# modemscope

Prometheus exporter for Hitron DOCSIS cable modems. Answers the question a
speed test can't: **was that outage my network, or the ISP's line?**

Verified against a **Hitron CODA-56** (DOCSIS 3.1, sw `7.3.5.3.2b1`) on Comcast.

## Why

The modem's own status pages carry the cable plant's vital signs — per-channel
SNR, transmit/receive power, and FEC error counters — plus the DOCSIS
registration state machine. None of it is retained: it's a live view that resets
whenever the modem reboots, and nothing polls it. So an intermittent problem
leaves no evidence behind, and by the time you look, the numbers are innocent.

modemscope polls it every scrape and hands Prometheus the history.

**The most important metric is `modemscope_uptime_seconds`.** Every error counter
the modem exposes resets on reboot, so "0 uncorrectables" means nothing on its
own — it might mean a clean line, or a modem that restarted a minute ago. A
silently rebooting modem is invisible to every other check you have: ping
recovers, the speed test passes, and the outage looks like it never happened.
Uptime is the only signal that distinguishes the two.

## Metrics

| Metric | Meaning |
| --- | --- |
| `modemscope_up` | 1 if the scrape succeeded. 0 = modem unreachable or rebooting. |
| `modemscope_uptime_seconds` | Modem uptime. **A drop = a reboot** (and a counter reset). |
| `modemscope_info{model,vendor,hw_version,sw_version,serial,rf_mac,config_name}` | Identity. Always 1. |
| `modemscope_downstream_snr_db{channel,port}` | Downstream SNR. <~33 dB risks uncorrectables on 256QAM. |
| `modemscope_downstream_power_dbmv{channel,port}` | Downstream power. Healthy ≈ −7..+7 dBmV. |
| `modemscope_downstream_uncorrectables_total{channel,port}` | **Uncorrectable codewords — the leading indicator of plant trouble.** |
| `modemscope_downstream_correcteds_total{channel,port}` | FEC-corrected codewords. |
| `modemscope_downstream_octets_total{channel,port}` | Downstream octets. |
| `modemscope_downstream_frequency_hz{channel,port}` | Channel centre frequency. |
| `modemscope_downstream_channels` | Bonded downstream channel count. A drop = channels fell off. |
| `modemscope_upstream_power_dbmv{channel,port}` | Upstream transmit power. Healthy ≈ 35..51 dBmV. |
| `modemscope_upstream_frequency_hz{channel,port}` / `..._bandwidth_hz{..}` | Upstream channel shape. |
| `modemscope_upstream_channels` | Bonded upstream channel count. |
| `modemscope_downstream_ofdm_locked{receiver}` | 1 if the OFDM receiver has PLC lock. |
| `modemscope_downstream_ofdm_snr_db{receiver}` | **DOCSIS 3.1 OFDM SNR.** |
| `modemscope_downstream_ofdm_plc_power_dbmv{receiver}` | OFDM PLC received power. |
| `modemscope_downstream_ofdm_uncorrectables_total{receiver}` | **Uncorrectables on the OFDM carrier — the single most important error signal on a 3.1 line.** |
| `modemscope_downstream_ofdm_correcteds_total{receiver}` | FEC-corrected codewords on the OFDM carrier. |
| `modemscope_downstream_ofdm_octets_total{receiver}` | OFDM octets. |
| `modemscope_downstream_ofdm_subcarrier0_hz{receiver}` | OFDM subcarrier-0 frequency. |
| `modemscope_upstream_ofdma_enabled{channel}` | 1 if upstream OFDMA is enabled (commonly 0 on Comcast; not a fault). |
| `modemscope_docsis_init_state{stage}` | 1 = healthy. Stages: `hw_init`, `find_downstream`, `ranging`, `dhcp`, `time_of_day`, `download_cfg`, `registration`, `bpi`, `traffic`. |
| `modemscope_network_access` | 1 if the CMTS permits the modem on the network. |
| `modemscope_scrape_duration_seconds` | Scrape latency (~0.2s typical). |

### Reading them

The counters **reset on reboot**, so always use `rate()` / `increase()` — they
handle resets correctly — and never compare raw totals across a restart.

```promql
# Uncorrectables appearing anywhere — QAM *or* OFDM. On DOCSIS 3.1 the OFDM
# carrier does most of the work and degrades first, so watching only the QAM
# channels can report a clean line while the real carrier is losing data.
sum(rate(modemscope_downstream_uncorrectables_total[15m]))
  + sum(rate(modemscope_downstream_ofdm_uncorrectables_total[15m])) > 0

# Did the errors arrive continuously, or in bursts around each reboot? This is
# what a point-in-time snapshot cannot tell you.
rate(modemscope_downstream_ofdm_uncorrectables_total[5m])

# The modem rebooted in the last 15m (uptime went backwards)
resets(modemscope_uptime_seconds[15m]) > 0

# Modem is re-registering — an ISP-side or line event, not your LAN
min(modemscope_docsis_init_state) == 0
```

## Config

| Env | Default | |
| --- | --- | --- |
| `MODEMSCOPE_LISTEN_ADDR` | `:9104` | metrics listen address |
| `MODEMSCOPE_MODEM_URL` | `https://192.168.100.1` | modem base URL |
| `MODEMSCOPE_TIMEOUT` | `8s` | total modem-I/O budget per scrape. Keep below Prometheus's scrape timeout, or a slow modem makes Prometheus give up before `modemscope_up=0` is delivered. |

`/metrics` exposes the collector; `/healthz` reports only that the exporter is
running — deliberately **not** whether the modem is reachable, since an
unreachable modem is a metric worth alerting on, not a reason to restart the pod.

## Notes on the modem

- The endpoints (`/data/getSysInfo.asp`, `dsinfo.asp`, `usinfo.asp`,
  `getCMInit.asp`, `getCmDocsisWan.asp`, `system_model.asp`) are **unauthenticated**.
- TLS verification is skipped: the modem presents a CableLabs-issued cert whose
  CN is its own MAC, which can't validate against a normal chain.
- It runs a small embedded GoAhead server. Endpoints are polled **sequentially**
  and the default scrape interval is conservative — it does not tolerate being
  hammered.
- The modem's `systemTime` can be badly skewed (its `timezone` is unset — observed
  ~1 h off). Don't derive anything from it; `uptime` is a duration and immune.
- `system_model.asp` returns a bare object; every other endpoint wraps a single
  object in an array.

## Develop

```sh
go test ./...
go run ./cmd/agent          # then: curl localhost:9104/metrics
make build                  # ghcr.io/gjcourt/modemscope:dev
```
