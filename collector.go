package main

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/superq/go-ping"
)

const (
	namespace = "smokeping"
)

var (
	labelNames = []string{"ip", "host"}

	pingResponseSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "response_duration_seconds",
			Help:      "A histogram of latencies for ping responses.",
			Buckets:   prometheus.ExponentialBuckets(0.00005, 2, 20),
		},
		labelNames,
	)
)

func init() {
	prometheus.MustRegister(pingResponseSeconds)
}

// SmokepingCollector collects metrics from the pinger.
type SmokepingCollector struct {
	pingers *[]*ping.Pinger

	requestsSent *prometheus.Desc
}

func NewSmokepingCollector(pingers *[]*ping.Pinger) *SmokepingCollector {
	for _, pinger := range *pingers {
		pinger.OnRecv = func(pkt *ping.Packet) {
			pingResponseSeconds.WithLabelValues(pkt.IPAddr.String(), pkt.Addr).Observe(pkt.Rtt.Seconds())
			fmt.Printf("%d bytes from %s: icmp_seq=%d time=%v\n",
				pkt.Nbytes, pkt.IPAddr, pkt.Seq, pkt.Rtt)
		}
		pinger.OnFinish = func(stats *ping.Statistics) {
			fmt.Printf("\n--- %s ping statistics ---\n", stats.Addr)
			fmt.Printf("%d packets transmitted, %d packets received, %v%% packet loss\n",
				stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss)
			fmt.Printf("round-trip min/avg/max/stddev = %v/%v/%v/%v\n",
				stats.MinRtt, stats.AvgRtt, stats.MaxRtt, stats.StdDevRtt)
		}
	}

	return &SmokepingCollector{
		pingers: pingers,
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
	for _, pinger := range *s.pingers {
		stats := pinger.Statistics()
	
		ch <- prometheus.MustNewConstMetric(
			s.requestsSent,
			prometheus.CounterValue,
			float64(stats.PacketsSent),
			stats.IPAddr.String(),
			stats.Addr,
		)
	}
}
