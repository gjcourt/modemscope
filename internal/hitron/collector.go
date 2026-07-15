package hitron

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const namespace = "modemscope"

// Collector scrapes the modem on each Prometheus scrape.
//
// The modem is polled inline rather than on a background ticker: the scrape
// takes ~200ms, and polling in lockstep with Prometheus means the sample
// timestamp matches when the value was actually read.
type Collector struct {
	client *Client
	log    *slog.Logger

	up             *prometheus.Desc
	scrapeDuration *prometheus.Desc
	info           *prometheus.Desc
	uptime         *prometheus.Desc

	dsSNR        *prometheus.Desc
	dsPower      *prometheus.Desc
	dsFreq       *prometheus.Desc
	dsOctets     *prometheus.Desc
	dsCorrected  *prometheus.Desc
	dsUncorrect  *prometheus.Desc
	dsChannels   *prometheus.Desc
	usPower      *prometheus.Desc
	usFreq       *prometheus.Desc
	usBandwidth  *prometheus.Desc
	usChannels   *prometheus.Desc
	initState    *prometheus.Desc
	networkAcces *prometheus.Desc
}

// NewCollector returns a Collector reading from client.
func NewCollector(client *Client, log *slog.Logger) *Collector {
	dsLabels := []string{"channel", "port"}
	usLabels := []string{"channel", "port"}

	return &Collector{
		client: client,
		log:    log,

		up: prometheus.NewDesc(namespace+"_up",
			"1 if the modem status endpoints were scraped successfully.", nil, nil),
		scrapeDuration: prometheus.NewDesc(namespace+"_scrape_duration_seconds",
			"Time taken to scrape the modem.", nil, nil),
		info: prometheus.NewDesc(namespace+"_info",
			"Modem identity. Always 1.",
			[]string{"model", "vendor", "hw_version", "sw_version", "serial", "rf_mac", "config_name"}, nil),
		uptime: prometheus.NewDesc(namespace+"_uptime_seconds",
			"Modem uptime. A drop means the modem rebooted, which also resets every error counter below.",
			nil, nil),

		dsSNR: prometheus.NewDesc(namespace+"_downstream_snr_db",
			"Downstream signal-to-noise ratio (dB). Below ~33 dB risks uncorrectable errors on 256QAM.",
			dsLabels, nil),
		dsPower: prometheus.NewDesc(namespace+"_downstream_power_dbmv",
			"Downstream received power (dBmV). Healthy range is roughly -7..+7.", dsLabels, nil),
		dsFreq: prometheus.NewDesc(namespace+"_downstream_frequency_hz",
			"Downstream channel centre frequency (Hz).", dsLabels, nil),
		dsOctets: prometheus.NewDesc(namespace+"_downstream_octets_total",
			"Downstream octets received. Resets when the modem reboots.", dsLabels, nil),
		dsCorrected: prometheus.NewDesc(namespace+"_downstream_correcteds_total",
			"FEC-corrected codewords. Resets when the modem reboots.", dsLabels, nil),
		dsUncorrect: prometheus.NewDesc(namespace+"_downstream_uncorrectables_total",
			"Uncorrectable codewords — the leading indicator of plant trouble. Resets when the modem reboots.",
			dsLabels, nil),
		dsChannels: prometheus.NewDesc(namespace+"_downstream_channels",
			"Number of bonded downstream channels. A drop means channels fell off.", nil, nil),

		usPower: prometheus.NewDesc(namespace+"_upstream_power_dbmv",
			"Upstream transmit power (dBmV). Healthy range is roughly 35..51; sustained highs mean the modem is straining.",
			usLabels, nil),
		usFreq: prometheus.NewDesc(namespace+"_upstream_frequency_hz",
			"Upstream channel centre frequency (Hz).", usLabels, nil),
		usBandwidth: prometheus.NewDesc(namespace+"_upstream_bandwidth_hz",
			"Upstream channel bandwidth (Hz).", usLabels, nil),
		usChannels: prometheus.NewDesc(namespace+"_upstream_channels",
			"Number of bonded upstream channels.", nil, nil),

		initState: prometheus.NewDesc(namespace+"_docsis_init_state",
			"DOCSIS registration stage: 1 if healthy, 0 otherwise. A stage flipping to 0 means the modem is re-registering.",
			[]string{"stage"}, nil),
		networkAcces: prometheus.NewDesc(namespace+"_network_access",
			"1 if the CMTS permits the modem on the network.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(c, ch)
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	status, err := c.client.Fetch(ctx)
	elapsed := time.Since(start).Seconds()

	ch <- prometheus.MustNewConstMetric(c.scrapeDuration, prometheus.GaugeValue, elapsed)
	if err != nil {
		// modemscope_up == 0 is itself the signal: the modem is unreachable or
		// rebooting. Emit nothing else so stale channel values can't look live.
		c.log.Warn("modem scrape failed", "err", err)
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1)

	ch <- prometheus.MustNewConstMetric(c.info, prometheus.GaugeValue, 1,
		status.Model.ModelName, status.Model.VendorName,
		status.SysInfo.HWVersion, status.SysInfo.SWVersion,
		status.SysInfo.SerialNumber, status.SysInfo.RFMac,
		status.DocsisWan.ConfigName)

	if d, uErr := ParseUptime(status.SysInfo.SystemUptime); uErr == nil {
		ch <- prometheus.MustNewConstMetric(c.uptime, prometheus.GaugeValue, d.Seconds())
	} else {
		c.log.Warn("could not parse uptime", "value", status.SysInfo.SystemUptime, "err", uErr)
	}

	ch <- prometheus.MustNewConstMetric(c.dsChannels, prometheus.GaugeValue, float64(len(status.Downstream)))
	for _, dc := range status.Downstream {
		lbl := []string{dc.ChannelID, dc.PortID}
		emit(ch, c.dsSNR, prometheus.GaugeValue, dc.SNR, lbl)
		emit(ch, c.dsPower, prometheus.GaugeValue, dc.SignalStrength, lbl)
		emit(ch, c.dsFreq, prometheus.GaugeValue, dc.Frequency, lbl)
		emit(ch, c.dsOctets, prometheus.CounterValue, dc.DSOctets, lbl)
		emit(ch, c.dsCorrected, prometheus.CounterValue, dc.Correcteds, lbl)
		emit(ch, c.dsUncorrect, prometheus.CounterValue, dc.Uncorrect, lbl)
	}

	ch <- prometheus.MustNewConstMetric(c.usChannels, prometheus.GaugeValue, float64(len(status.Upstream)))
	for _, uc := range status.Upstream {
		lbl := []string{uc.ChannelID, uc.PortID}
		emit(ch, c.usPower, prometheus.GaugeValue, uc.SignalStrength, lbl)
		emit(ch, c.usFreq, prometheus.GaugeValue, uc.Frequency, lbl)
		emit(ch, c.usBandwidth, prometheus.GaugeValue, uc.Bandwidth, lbl)
	}

	for stage, v := range map[string]string{
		"hw_init":         status.CMInit.HWInit,
		"find_downstream": status.CMInit.FindDownstream,
		"ranging":         status.CMInit.Ranging,
		"dhcp":            status.CMInit.DHCP,
		"time_of_day":     status.CMInit.TimeOfDay,
		"download_cfg":    status.CMInit.DownloadCfg,
		"registration":    status.CMInit.Registration,
		"bpi":             status.CMInit.BPIStatus,
		"traffic":         status.CMInit.TrafficStatus,
	} {
		ch <- prometheus.MustNewConstMetric(c.initState, prometheus.GaugeValue, boolToFloat(isSuccess(v)), stage)
	}

	ch <- prometheus.MustNewConstMetric(c.networkAcces, prometheus.GaugeValue,
		boolToFloat(isSuccess(status.CMInit.NetworkAccess)))
}

// emit skips the metric when the firmware gives a non-numeric placeholder,
// rather than reporting a misleading zero.
func emit(ch chan<- prometheus.Metric, d *prometheus.Desc, t prometheus.ValueType, raw string, labels []string) {
	v, ok := parseFloat(raw)
	if !ok {
		return
	}
	ch <- prometheus.MustNewConstMetric(d, t, v, labels...)
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
