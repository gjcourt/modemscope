# modemscope

Prometheus exporter for Hitron DOCSIS cable modems. It polls the modem's own
status pages — the ones a speed test never looks at — and turns per-channel
SNR, transmit/receive power, and FEC error counters into a queryable history,
so an intermittent line problem leaves evidence behind instead of vanishing
the moment the modem reboots.

Every error counter the modem exposes resets on reboot, so
`modemscope_uptime_seconds` is what makes the rest of them interpretable: "0
uncorrectables" means nothing on its own — it could be a clean line, or a
modem that restarted a minute ago.

Verified against a **Hitron CODA-56** (DOCSIS 3.1, sw `7.3.5.3.2b1`) on
Comcast. Serves metrics on `:9104`.

> For how the exporter is built — components, scrape flow, the modem
> interface, and design decisions — see [`ARCHITECTURE.md`](ARCHITECTURE.md).

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
| `modemscope_downstream_ofdm_locked{receiver}` | 1 if the OFDM receiver holds all three locks (PLC, NCP, MDC1). |
| `modemscope_downstream_ofdm_snr_db{receiver}` | **DOCSIS 3.1 OFDM SNR.** |
| `modemscope_downstream_ofdm_plc_power_dbmv{receiver}` | OFDM PLC received power. |
| `modemscope_downstream_ofdm_uncorrectables_total{receiver}` | **Uncorrectables on the OFDM carrier — the single most important error signal on a 3.1 line.** |
| `modemscope_downstream_ofdm_correcteds_total{receiver}` | FEC-corrected codewords on the OFDM carrier. |
| `modemscope_downstream_ofdm_octets_total{receiver}` | OFDM octets. |
| `modemscope_downstream_ofdm_subcarrier0_hz{receiver}` | OFDM subcarrier-0 frequency. |
| `modemscope_upstream_ofdma_enabled{channel}` | 1 if upstream OFDMA is enabled (commonly 0 on Comcast; not a fault). |
| `modemscope_upstream_ofdma_frequency_hz{channel}` / `..._power_dbmv` / `..._bandwidth_hz` | Upstream OFDMA shape; absent when the channel is disabled. |
| `modemscope_downstream_ofdm_lock{receiver,stage}` | Per-stage OFDM lock (`plc`, `ncp`, `mdc1`). **`plc=1` with `ncp=0`/`mdc1=0` is a partial lock: values look real but counters freeze, so `rate()` misreads it as a clean carrier.** |
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

## Configuration

Three environment variables, all optional:

| Env | Default | |
| --- | --- | --- |
| `MODEMSCOPE_LISTEN_ADDR` | `:9104` | metrics listen address |
| `MODEMSCOPE_MODEM_URL` | `https://192.168.100.1` | modem base URL |
| `MODEMSCOPE_TIMEOUT` | `8s` | total modem-I/O budget per scrape. Keep below Prometheus's scrape timeout, or a slow modem makes Prometheus give up before `modemscope_up=0` is delivered. |

## Running / Deployment

```sh
go run ./cmd/agent
curl localhost:9104/metrics
```

`/metrics` exposes the collector; `/healthz` reports only that the exporter is
running — deliberately **not** whether the modem is reachable, since an
unreachable modem is a metric worth alerting on, not a reason to restart the
pod.

CI builds and pushes the image to `ghcr.io/gjcourt/modemscope` on every push
to `main`, tagging it additively as `main`, `<sha7>`, `YYYY-MM-DD`, the
immutable `YYYY-MM-DD-<sha7>`, and `latest`. In the homelab it runs as a
single-replica Deployment, pinned by tag and digest in
`homelab/apps/base/modemscope/deployment.yaml` and rolled out via GitOps —
bump the pin there to deploy a new build, don't repoint `latest`.

## Notes on the modem

- The endpoints (`/data/getSysInfo.asp`, `dsinfo.asp`, `usinfo.asp`,
  `dsofdminfo.asp`, `usofdminfo.asp`, `getCMInit.asp`, `getCmDocsisWan.asp`,
  `system_model.asp`) are **unauthenticated**.
- TLS verification is skipped: the modem presents a CableLabs-issued cert whose
  CN is its own MAC, which can't validate against a normal chain.
- It runs a small embedded GoAhead server. Endpoints are polled **sequentially**
  and the default scrape interval is conservative — it does not tolerate being
  hammered.
- The modem's `systemTime` can be badly skewed (its `timezone` is unset — observed
  ~1 h off). Don't derive anything from it; `uptime` is a duration and immune.
- `system_model.asp` returns a bare object; every other endpoint wraps a single
  object in an array.

## Development

Go 1.23.

```sh
go test ./...
go vet ./...
gofmt -l .
make build   # docker buildx build ... -t ghcr.io/gjcourt/modemscope:dev
make tidy    # go mod tidy
```

CI (`.github/workflows/build.yml`) runs gofmt, `go vet`, `go test`, and a
`go mod tidy` diff check on every pull request and on push to `main`.
