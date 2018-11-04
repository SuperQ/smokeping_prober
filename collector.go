package main

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/superq/go-ping"
)

const (
	namespace = "smokeping"
)

func secondsDurationBuckets() []float64 {
	return []float64{
		0.00005, /* 50us */
		0.0001,  /* 100us */
		0.00025, /* 250us */
		0.0005,  /* 500us */
		0.001,   /* 1ms */
		0.0025,  /* 2.5ms */
		0.005,   /* 5ms */
		0.025,   /* 25ms */
		0.1,     /* 100ms */
		0.25,    /* 250ms */
		0.5,     /* 500ms */
		1.0,     /* 1s */
		10.0,    /* 10s */
		30.0,    /* 30s */
		60.0,    /* 1m */
		300.0,   /* 10m */
	}
}

var (
	labelNames = []string{"ip", "host"}

	pingResponseSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "response_duration_seconds",
			Help:      "A histogram of latencies for ping responses.",
			Buckets:   secondsDurationBuckets(),
		},
		labelNames,
	)
)

func init() {
	prometheus.MustRegister(pingResponseSeconds)
}

// SmokepingCollector collects metrics from the pinger.
type SmokepingCollector struct {
	pinger *ping.Pinger

	requestsSent *prometheus.Desc
}

func NewSmokepingCollector(pinger *ping.Pinger) *SmokepingCollector {
	pinger.OnRecv = func(pkt *ping.Packet) {
		pingResponseSeconds.WithLabelValues(pkt.IPAddr.String(), pkt.Addr).Observe(pkt.Rtt.Seconds())
		fmt.Printf("%d bytes from %s: icmp_seq=%d time=%v\n",
			pkt.Nbytes, pkt.IPAddr, pkt.Seq, pkt.Rtt)
	}

	return &SmokepingCollector{
		pinger: pinger,
		requestsSent: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "requests_total"),
			"Number of ping requests sent",
			labelNames,
			nil,
		),
	}
}

func (s *SmokepingCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- s.requestsSent
}

func (s *SmokepingCollector) Collect(ch chan<- prometheus.Metric) {
	stats := s.pinger.Statistics()

	ch <- prometheus.MustNewConstMetric(
		s.requestsSent,
		prometheus.CounterValue,
		float64(stats.PacketsSent),
		stats.IPAddr.String(),
		stats.Addr,
	)
}
