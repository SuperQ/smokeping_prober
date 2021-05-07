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
	"errors"
	"fmt"
	"math"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-ping/ping"
	"github.com/superq/smokeping_prober/config"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promlog"
	"github.com/prometheus/common/promlog/flag"
	"github.com/prometheus/common/version"
	"golang.org/x/sync/errgroup"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	// Generated with: prometheus.ExponentialBuckets(0.00005, 2, 20)
	defaultBuckets = "5e-05,0.0001,0.0002,0.0004,0.0008,0.0016,0.0032,0.0064,0.0128,0.0256,0.0512,0.1024,0.2048,0.4096,0.8192,1.6384,3.2768,6.5536,13.1072,26.2144"

	logger log.Logger

	sc = &config.SafeConfig{
		C: &config.Config{},
	}
)

type hostList []string

func (h *hostList) Set(value string) error {
	if value == "" {
		return fmt.Errorf("'%s' is not valid hostname", value)
	} else {
		*h = append(*h, value)
		return nil
	}
}

func (h *hostList) String() string {
	return ""
}

func (h *hostList) IsCumulative() bool {
	return true
}

func HostList(s kingpin.Settings) (target *[]string) {
	target = new([]string)
	s.SetValue((*hostList)(target))
	return
}

func init() {
	prometheus.MustRegister(version.NewCollector("smokeping_prober"))
}

func parseBuckets(buckets string) ([]float64, error) {
	bucketstrings := strings.Split(buckets, ",")
	bucketlist := make([]float64, len(bucketstrings))
	for i := range bucketstrings {
		value, err := strconv.ParseFloat(bucketstrings[i], 64)
		if err != nil {
			return nil, err
		}
		bucketlist[i] = value
	}
	return bucketlist, nil
}

func main() {
	var (
		configFile    = kingpin.Flag("config.file", "Optional smokeping_prober configuration yaml file.").String()
		listenAddress = kingpin.Flag("web.listen-address", "Address on which to expose metrics and web interface.").Default(":9374").String()
		metricsPath   = kingpin.Flag("web.telemetry-path", "Path under which to expose metrics.").Default("/metrics").String()

		buckets    = kingpin.Flag("buckets", "A comma delimited list of buckets to use").Default(defaultBuckets).String()
		interval   = kingpin.Flag("ping.interval", "Ping interval duration").Short('i').Default("1s").Duration()
		privileged = kingpin.Flag("privileged", "Run in privileged ICMP mode").Default("true").Bool()
		hosts      = HostList(kingpin.Arg("hosts", "List of hosts to ping"))
	)

	promlogConfig := &promlog.Config{}
	flag.AddFlags(kingpin.CommandLine, promlogConfig)
	kingpin.Version(version.Print("smokeping_prober"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()
	logger = promlog.New(promlogConfig)

	level.Info(logger).Log("msg", "Starting smokeping_prober", "version", version.Info())
	level.Info(logger).Log("msg", "Build context", "build_context", version.BuildContext())

	if err := sc.ReloadConfig(*configFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			level.Info(logger).Log("msg", "ignoring missing config file", "filename", *configFile)
		} else {
			level.Error(logger).Log("msg", "error loading config", "filename", err.Error())
			return
		}
	}

	bucketlist, err := parseBuckets(*buckets)
	if err != nil {
		level.Error(logger).Log("msg", "Failed to parse buckets", "err", err)
		return
	}
	pingResponseSeconds := newPingResponseHistogram(bucketlist)
	prometheus.MustRegister(pingResponseSeconds)

	pingers := make([]*ping.Pinger, len(*hosts))
	var pinger *ping.Pinger
	var host string
	for i, host := range *hosts {
		pinger = ping.New(host)

		err := pinger.Resolve()
		if err != nil {
			level.Error(logger).Log("msg", "Failed to resolve pinger", "err", err)
			return
		}

		level.Info(logger).Log("msg", "Pinger resolved", "host", host, "ip_addr", pinger.IPAddr())

		pinger.Interval = *interval
		pinger.Timeout = time.Duration(math.MaxInt64)
		pinger.RecordRtts = false
		pinger.SetPrivileged(*privileged)

		pingers[i] = pinger
	}

	maxInterval := *interval

	sc.Lock()
	for _, targetGroup := range sc.C.Targets {
		if targetGroup.Interval > maxInterval {
			maxInterval = targetGroup.Interval
		}
		for _, host = range targetGroup.Hosts {
			pinger = ping.New(host)
			pinger.Interval = targetGroup.Interval
			pinger.SetNetwork(targetGroup.Network)
			if targetGroup.Protocol == "icmp" {
				pinger.SetPrivileged(true)
			}
			err := pinger.Resolve()
			if err != nil {
				level.Error(logger).Log("msg", "failed to resolve pinger", "error", err.Error())
				return
			}
			pingers = append(pingers, pinger)
		}
	}
	sc.Unlock()

	if len(pingers) == 0 {
		level.Error(logger).Log("msg", "no targets specified on command line or in config file")
		return
	}

	splay := time.Duration(interval.Nanoseconds() / int64(len(pingers)))
	level.Info(logger).Log("msg", fmt.Sprintf("Waiting %s between starting pingers", splay))
	g := new(errgroup.Group)
	for _, pinger := range pingers {
		level.Info(logger).Log("msg", "Starting prober", "address", pinger.Addr(), "interval", pinger.Interval)
		g.Go(pinger.Run)
		time.Sleep(splay)
	}

	prometheus.MustRegister(NewSmokepingCollector(&pingers, *pingResponseSeconds))

	http.Handle(*metricsPath, promhttp.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
			<head><title>Smokeping Exporter</title></head>
			<body>
			<h1>Smokeping Exporter</h1>
			<p><a href="` + *metricsPath + `">Metrics</a></p>
			</body>
			</html>`))
	})

	level.Info(logger).Log("msg", "Listening on", "address", *listenAddress)
	if err := http.ListenAndServe(*listenAddress, nil); err != nil {
		level.Error(logger).Log("err", err)
		os.Exit(1)
	}

	for _, pinger := range pingers {
		pinger.Stop()
	}
	if err = g.Wait(); err != nil {
		level.Error(logger).Log("msg", "pingers failed", "error", err)
		os.Exit(1)
	}
}
