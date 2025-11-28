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
	pingResponseTTL        *prometheus.GaugeVec
	pingResponseDuplicates *prometheus.CounterVec
	pingRecvErrors         prometheus.Counter
	pingSendErrors         *prometheus.CounterVec
)

// initMetrics initializes (or re-initializes) the metric vectors with the provided label set.
func initMetrics(labelNames []string, buckets []float64, factor float64) *prometheus.HistogramVec {
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

// SmokepingCollector collects metrics from the probes and their pingers.
type SmokepingCollector struct {
	probes *[]probe

	requestsSent *prometheus.Desc
	labelNames   []string
}

func NewSmokepingCollector(probes []probe, labelNames []string, pingResponseSeconds prometheus.HistogramVec) *SmokepingCollector {
	instance := SmokepingCollector{
		requestsSent: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "requests_total"),
			"Number of ping requests sent",
			labelNames,
			nil,
		),
		labelNames: labelNames,
	}

	instance.updateProbes(probes, pingResponseSeconds)

	return &instance
}

func (s *SmokepingCollector) buildLabelValues(pr *probe, overrideIP string) []string {
	vals := make([]string, 0, len(s.labelNames))
	base := map[string]string{
		"ip":     pr.pinger.IPAddr().String(),
		"host":   pr.pinger.Addr(),
		"source": pr.pinger.Source,
		"tos":    strconv.Itoa(int(pr.pinger.TrafficClass())),
	}
	labels := pr.labels
	for _, k := range s.labelNames {
		if v, ok := base[k]; ok {
			vals = append(vals, v)
			continue
		}
		vals = append(vals, labels[k])
	}
	if overrideIP != "" {
		for i, k := range s.labelNames {
			if k == "ip" {
				vals[i] = overrideIP
				break
			}
		}
	}
	return vals
}

func (s *SmokepingCollector) updateProbes(probes []probe, pingResponseSeconds prometheus.HistogramVec) {
	pingResponseDuplicates.Reset()
	pingResponseSeconds.Reset()
	pingResponseTTL.Reset()
	pingSendErrors.Reset()
	for _, pr := range probes {
		// Init all metrics to 0s.
		vals := s.buildLabelValues(&pr, "")
		pingResponseDuplicates.WithLabelValues(vals...)
		pingResponseSeconds.WithLabelValues(vals...)
		pingResponseTTL.WithLabelValues(vals...)
		pingSendErrors.WithLabelValues(vals...)

		p := pr

		// Setup handler functions.
		p.pinger.OnRecv = func(pkt *probing.Packet) {
			vals := s.buildLabelValues(&p, pkt.IPAddr.String())
			pingResponseSeconds.WithLabelValues(vals...).Observe(pkt.Rtt.Seconds())
			pingResponseTTL.WithLabelValues(vals...).Set(float64(pkt.TTL))
			logger.Debug("Echo reply", "ip_addr", pkt.IPAddr,
				"bytes_received", pkt.Nbytes, "icmp_seq", pkt.Seq, "rtt", pkt.Rtt, "ttl", pkt.TTL)
		}
		p.pinger.OnDuplicateRecv = func(pkt *probing.Packet) {
			vals := s.buildLabelValues(&p, pkt.IPAddr.String())
			pingResponseDuplicates.WithLabelValues(vals...).Inc()
			logger.Debug("Echo reply (DUP!)", "ip_addr", pkt.IPAddr,
				"bytes_received", pkt.Nbytes, "icmp_seq", pkt.Seq, "rtt", pkt.Rtt, "ttl", pkt.TTL)
		}
		p.pinger.OnFinish = func(stats *probing.Statistics) {
			logger.Debug("Ping statistics", "addr", stats.Addr,
				"packets_sent", stats.PacketsSent, "packets_received", stats.PacketsRecv,
				"packet_loss_percent", stats.PacketLoss, "min_rtt", stats.MinRtt, "avg_rtt",
				stats.AvgRtt, "max_rtt", stats.MaxRtt, "stddev_rtt", stats.StdDevRtt)
		}
		p.pinger.OnRecvError = func(err error) {
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
		p.pinger.OnSendError = func(pkt *probing.Packet, err error) {
			vals := s.buildLabelValues(&p, pkt.IPAddr.String())
			pingSendErrors.WithLabelValues(vals...).Inc()
			logger.Debug("Error sending packet", "ip_addr", pkt.IPAddr,
				"bytes_received", pkt.Nbytes, "icmp_seq", pkt.Seq, "rtt", pkt.Rtt, "ttl", pkt.TTL, "error", err)
		}
	}
	s.probes = &probes
}

func (s *SmokepingCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- s.requestsSent
}

func (s *SmokepingCollector) Collect(ch chan<- prometheus.Metric) {
	for _, pr := range *s.probes {
		stats := pr.pinger.Statistics()
		vals := s.buildLabelValues(&pr, stats.IPAddr.String())
		ch <- prometheus.MustNewConstMetric(
			s.requestsSent,
			prometheus.CounterValue,
			float64(stats.PacketsSent),
			vals...,
		)
	}
}
