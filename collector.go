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
)

const (
	namespace = "smokeping"
)

var (
	labelNames = []string{"ip", "host", "source", "tos"}

	pingResponseTTL        *prometheus.GaugeVec
	pingResponseDuplicates *prometheus.CounterVec
	pingRecvErrors         prometheus.Counter
	pingSendErrors         *prometheus.CounterVec

	labelsByPinger map[*probing.Pinger]map[string]string
)

// initializes (or re-initializes) the metric vectors with the provided label set.
func InitMetrics(newLabelNames []string, buckets []float64, factor float64) *prometheus.HistogramVec {

	if pingResponseTTL != nil {
		prometheus.Unregister(pingResponseTTL)
	}
	if pingResponseDuplicates != nil {
		prometheus.Unregister(pingResponseDuplicates)
	}
	if pingRecvErrors != nil {
		prometheus.Unregister(pingRecvErrors)
	}
	if pingSendErrors != nil {
		prometheus.Unregister(pingSendErrors)
	}

	labelNames = newLabelNames

	pingResponseTTL = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "response_ttl",
			Help:      "The last response Time To Live (TTL).",
		},
		labelNames,
	)
	pingResponseDuplicates = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "response_duplicates_total",
			Help:      "The number of duplicated response packets.",
		},
		labelNames,
	)
	pingRecvErrors = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "receive_errors_total",
			Help:      "The number of errors when Pinger attempts to receive packets.",
		},
	)
	pingSendErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "send_errors_total",
			Help:      "The number of errors when Pinger attempts to send packets.",
		},
		labelNames,
	)

	prometheus.MustRegister(pingResponseTTL)
	prometheus.MustRegister(pingResponseDuplicates)
	prometheus.MustRegister(pingRecvErrors)
	prometheus.MustRegister(pingSendErrors)

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

func SetLabelsForPinger(p *probing.Pinger, labels map[string]string) {
	if labelsByPinger == nil {
		labelsByPinger = make(map[*probing.Pinger]map[string]string)
	}
	labelsByPinger[p] = labels
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

func buildLabelValues(pinger *probing.Pinger, overrideIP string) []string {
	vals := make([]string, 0, len(labelNames))
	base := map[string]string{
		"ip":     pinger.IPAddr().String(),
		"host":   pinger.Addr(),
		"source": pinger.Source,
		"tos":    strconv.Itoa(int(pinger.TrafficClass())),
	}
	labels := labelsByPinger[pinger]
	for _, k := range labelNames {
		if v, ok := base[k]; ok {
			vals = append(vals, v)
			continue
		}
		vals = append(vals, labels[k])
	}
	if overrideIP != "" {
		for i, k := range labelNames {
			if k == "ip" {
				vals[i] = overrideIP
				break
			}
		}
	}
	return vals
}

func (s *SmokepingCollector) updatePingers(pingers []*probing.Pinger, pingResponseSeconds prometheus.HistogramVec) {
	pingResponseDuplicates.Reset()
	pingResponseSeconds.Reset()
	pingResponseTTL.Reset()
	pingSendErrors.Reset()
	for _, pinger := range pingers {
		// Init all metrics to 0s.
		vals := buildLabelValues(pinger, "")
		pingResponseDuplicates.WithLabelValues(vals...)
		pingResponseSeconds.WithLabelValues(vals...)
		pingResponseTTL.WithLabelValues(vals...)
		pingSendErrors.WithLabelValues(vals...)

		p := pinger

		// Setup handler functions.
		p.OnRecv = func(pkt *probing.Packet) {
			vals := buildLabelValues(p, pkt.IPAddr.String())
			pingResponseSeconds.WithLabelValues(vals...).Observe(pkt.Rtt.Seconds())
			pingResponseTTL.WithLabelValues(vals...).Set(float64(pkt.TTL))
			logger.Debug("Echo reply", "ip_addr", pkt.IPAddr,
				"bytes_received", pkt.Nbytes, "icmp_seq", pkt.Seq, "rtt", pkt.Rtt, "ttl", pkt.TTL)
		}
		p.OnDuplicateRecv = func(pkt *probing.Packet) {
			vals := buildLabelValues(p, pkt.IPAddr.String())
			pingResponseDuplicates.WithLabelValues(vals...).Inc()
			logger.Debug("Echo reply (DUP!)", "ip_addr", pkt.IPAddr,
				"bytes_received", pkt.Nbytes, "icmp_seq", pkt.Seq, "rtt", pkt.Rtt, "ttl", pkt.TTL)
		}
		p.OnFinish = func(stats *probing.Statistics) {
			logger.Debug("Ping statistics", "addr", stats.Addr,
				"packets_sent", stats.PacketsSent, "packets_received", stats.PacketsRecv,
				"packet_loss_percent", stats.PacketLoss, "min_rtt", stats.MinRtt, "avg_rtt",
				stats.AvgRtt, "max_rtt", stats.MaxRtt, "stddev_rtt", stats.StdDevRtt)
		}
		p.OnRecvError = func(err error) {
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
		p.OnSendError = func(pkt *probing.Packet, err error) {
			vals := buildLabelValues(p, pkt.IPAddr.String())
			pingSendErrors.WithLabelValues(vals...).Inc()
			logger.Debug("Error sending packet", "ip_addr", pkt.IPAddr,
				"bytes_received", pkt.Nbytes, "icmp_seq", pkt.Seq, "rtt", pkt.Rtt, "ttl", pkt.TTL, "error", err)
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
		vals := buildLabelValues(pinger, stats.IPAddr.String())
		ch <- prometheus.MustNewConstMetric(
			s.requestsSent,
			prometheus.CounterValue,
			float64(stats.PacketsSent),
			vals...,
		)
	}
}
