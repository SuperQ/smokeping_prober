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
	"net"
	"strconv"

	probing "github.com/prometheus-community/pro-bing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	namespace = "smokeping"
)

var (
	labelNames = []string{"ip", "host", "source", "tos"}

	pingResponseTTL = promauto.NewGaugeVec(
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
	pingRecvErrors = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "receive_errors_total",
			Help:      "The number of errors when Pinger attempts to receive packets.",
		},
	)
	pingSendErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "send_errors_total",
			Help:      "The number of errors when Pinger attempts to send packets.",
		},
		labelNames,
	)
)

func newPingResponseHistogram(buckets []float64, factor float64) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace:                   namespace,
			Name:                        "response_duration_seconds",
			Help:                        "A histogram of latencies for ping responses.",
			Buckets:                     buckets,
			NativeHistogramBucketFactor: factor,
		},
		labelNames,
	)
}

// SmokepingCollector collects metrics from the pinger.
type SmokepingCollector struct {
	pingers *[]*probing.Pinger

	requestsSent *prometheus.Desc
}

func NewSmokepingCollector(pingers []*probing.Pinger, pingResponseSeconds prometheus.HistogramVec) *SmokepingCollector {

	instance := SmokepingCollector{
		requestsSent: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "requests_total"),
			"Number of ping requests sent",
			labelNames,
			nil,
		),
	}

	instance.updatePingers(pingers, pingResponseSeconds)

	return &instance
}

func (s *SmokepingCollector) updatePingers(pingers []*probing.Pinger, pingResponseSeconds prometheus.HistogramVec) {
	pingResponseDuplicates.Reset()
	pingResponseSeconds.Reset()
	pingResponseTTL.Reset()
	pingSendErrors.Reset()
	for _, pinger := range pingers {
		// Init all metrics to 0s.
		ipAddr := pinger.IPAddr().String()
		host := pinger.Addr()
		source := pinger.Source
		tos := strconv.Itoa(int(pinger.TrafficClass()))
		pingResponseDuplicates.WithLabelValues(ipAddr, host, source, tos)
		pingResponseSeconds.WithLabelValues(ipAddr, host, source, tos)
		pingResponseTTL.WithLabelValues(ipAddr, host, source, tos)
		pingSendErrors.WithLabelValues(ipAddr, host, source, tos)

		// Setup handler functions.
		pinger.OnRecv = func(pkt *probing.Packet) {
			pingResponseSeconds.WithLabelValues(pkt.IPAddr.String(), host, source, tos).Observe(pkt.Rtt.Seconds())
			pingResponseTTL.WithLabelValues(pkt.IPAddr.String(), host, source, tos).Set(float64(pkt.TTL))
			logger.Debug("Echo reply", "ip_addr", pkt.IPAddr,
				"bytes_received", pkt.Nbytes, "icmp_seq", pkt.Seq, "rtt", pkt.Rtt, "ttl", pkt.TTL, "tos", tos)
		}
		pinger.OnDuplicateRecv = func(pkt *probing.Packet) {
			pingResponseDuplicates.WithLabelValues(pkt.IPAddr.String(), host, source, tos).Inc()
			logger.Debug("Echo reply (DUP!)", "ip_addr", pkt.IPAddr,
				"bytes_received", pkt.Nbytes, "icmp_seq", pkt.Seq, "rtt", pkt.Rtt, "ttl", pkt.TTL, "tos", tos)
		}
		pinger.OnFinish = func(stats *probing.Statistics) {
			logger.Debug("Ping statistics", "addr", stats.Addr,
				"packets_sent", stats.PacketsSent, "packets_received", stats.PacketsRecv,
				"packet_loss_percent", stats.PacketLoss, "min_rtt", stats.MinRtt, "avg_rtt",
				stats.AvgRtt, "max_rtt", stats.MaxRtt, "stddev_rtt", stats.StdDevRtt)
		}
		pinger.OnRecvError = func(err error) {
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Timeout() {
					// Ignore read timeout errors, these are handled by the pinger.
					return
				}
			}
			pingRecvErrors.Inc()
			// TODO: @tjhop -- should this be logged at error level?
			logger.Debug("Error receiving packet", "error", err)
		}
		pinger.OnSendError = func(pkt *probing.Packet, err error) {
			pingSendErrors.WithLabelValues(pkt.IPAddr.String(), host, source, tos).Inc()
			logger.Debug("Error sending packet", "ip_addr", pkt.IPAddr,
				"bytes_received", pkt.Nbytes, "icmp_seq", pkt.Seq, "rtt", pkt.Rtt, "ttl", pkt.TTL, "tos", tos, "error", err)
		}
	}
	s.pingers = &pingers
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
			pinger.Source,
			strconv.Itoa(int(pinger.TrafficClass())),
		)
	}
}
