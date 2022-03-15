// Copyright 2018 Ben Kochie <superq@gmail.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"github.com/go-ping/ping"

	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	namespace = "smokeping"
)

var (
	labelNames = []string{"ip", "host"}

	pingResponseTtl = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "response_ttl",
			Help:      "The last response Time To Live (TTL).",
		},
		labelNames,
	)
	pingResponseDuplicates = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "response_duplicates_total",
			Help:      "The number of duplicated response packets.",
		},
		labelNames,
	)
)

func newPingResponseHistogram(buckets []float64) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "response_duration_seconds",
			Help:      "A histogram of latencies for ping responses.",
			Buckets:   buckets,
		},
		labelNames,
	)
}

// SmokepingCollector collects metrics from the pinger.
type SmokepingCollector struct {
	pingers *[]*ping.Pinger

	requestsSent *prometheus.Desc
}

func NewSmokepingCollector(pingers *[]*ping.Pinger, pingResponseSeconds prometheus.HistogramVec) *SmokepingCollector {
	for _, pinger := range *pingers {
		// Init all metrics to 0s.
		ipAddr := pinger.IPAddr().String()
		pingResponseDuplicates.WithLabelValues(ipAddr, pinger.Addr())
		pingResponseSeconds.WithLabelValues(ipAddr, pinger.Addr())
		pingResponseTtl.WithLabelValues(ipAddr, pinger.Addr())

		// Setup handler functions.
		pinger.OnRecv = func(pkt *ping.Packet) {
			pingResponseSeconds.WithLabelValues(pkt.IPAddr.String(), pkt.Addr).Observe(pkt.Rtt.Seconds())
			pingResponseTtl.WithLabelValues(pkt.IPAddr.String(), pkt.Addr).Set(float64(pkt.Ttl))
			level.Debug(logger).Log("msg", "Echo reply", "ip_addr", pkt.IPAddr,
				"bytes_received", pkt.Nbytes, "icmp_seq", pkt.Seq, "time", pkt.Rtt, "ttl", pkt.Ttl)
		}
		pinger.OnDuplicateRecv = func(pkt *ping.Packet) {
			pingResponseDuplicates.WithLabelValues(pkt.IPAddr.String(), pkt.Addr).Inc()
			level.Debug(logger).Log("msg", "Echo reply (DUP!)", "ip_addr", pkt.IPAddr,
				"bytes_received", pkt.Nbytes, "icmp_seq", pkt.Seq, "time", pkt.Rtt, "ttl", pkt.Ttl)
		}
		pinger.OnFinish = func(stats *ping.Statistics) {
			level.Debug(logger).Log("msg", "Ping statistics", "addr", stats.Addr,
				"packets_sent", stats.PacketsSent, "packets_received", stats.PacketsRecv,
				"packet_loss_percent", stats.PacketLoss, "min_rtt", stats.MinRtt, "avg_rtt",
				stats.AvgRtt, "max_rtt", stats.MaxRtt, "stddev_rtt", stats.StdDevRtt)
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
