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
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	probing "github.com/prometheus-community/pro-bing"
	"github.com/superq/smokeping_prober/config"

	"github.com/alecthomas/kingpin/v2"
	"github.com/prometheus/client_golang/prometheus"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promslog"
	"github.com/prometheus/common/promslog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	"github.com/prometheus/exporter-toolkit/web/kingpinflag"
	"go.uber.org/automaxprocs/maxprocs"
	"golang.org/x/sync/errgroup"
)

var (
	// Generated with: prometheus.ExponentialBuckets(0.00005, 2, 20)
	defaultBuckets = "5e-05,0.0001,0.0002,0.0004,0.0008,0.0016,0.0032,0.0064,0.0128,0.0256,0.0512,0.1024,0.2048,0.4096,0.8192,1.6384,3.2768,6.5536,13.1072,26.2144"

	logger *slog.Logger

	sc = &config.SafeConfig{
		C: &config.Config{},
	}
)

type hostList []string

func (h *hostList) Set(value string) error {
	if value == "" {
		return fmt.Errorf("'%s' is not valid hostname", value)
	}
	*h = append(*h, value)
	return nil
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

type smokePingers struct {
	started     []*probing.Pinger
	prepared    []*probing.Pinger
	g           *errgroup.Group
	maxInterval time.Duration
	resolver    *TTLAwareDNSResolver
}

func (s *smokePingers) sizeOfPrepared() int {
	if s.prepared != nil {
		return len(s.prepared)
	}
	return 0
}

func (s *smokePingers) start() {
	if s.sizeOfPrepared() == 0 {
		return
	}
	if s.g != nil {
		err := s.stop()
		if err != nil {
			logger.Warn("At least one previous pinger failed to run", "err", err)
		}
	}
	s.g = new(errgroup.Group)
	s.started = s.prepared
	splay := time.Duration(s.maxInterval.Nanoseconds() / int64(len(s.started)))
	for _, pinger := range s.started {
		logger.Info("Starting prober", "address", pinger.Addr(), "interval", pinger.Interval, "size_bytes", pinger.Size, "source_address", pinger.Source)
		s.g.Go(pinger.Run)
		time.Sleep(splay)
	}
	s.prepared = nil
}

func (s *smokePingers) stop() error {
	if s.g == nil {
		return nil
	}
	if s.started == nil {
		return nil
	}
	for _, pinger := range s.started {
		pinger.Stop()
	}
	if s.resolver != nil {
		s.resolver.Stop()
	}
	if err := s.g.Wait(); err != nil {
		return fmt.Errorf("pingers failed: %v", err)
	}
	return nil
}

func (s *smokePingers) prepare(hosts *[]string, interval *time.Duration, privileged *bool, sizeBytes *int, tosField *uint8) error {
	pingers := make([]*probing.Pinger, len(*hosts))
	var pinger *probing.Pinger
	var host string
	for i, host := range *hosts {
		pinger = probing.New(host)

		err := pinger.Resolve()
		if err != nil {
			return fmt.Errorf("failed to resolve pinger: %v", err)
		}

		logger.Info("Pinger resolved", "host", host, "ip_addr", pinger.IPAddr())

		pinger.Interval = *interval
		pinger.RecordRtts = false
		pinger.SetPrivileged(*privileged)
		pinger.Size = *sizeBytes
		pinger.SetTrafficClass(*tosField)

		pingers[i] = pinger
	}

	maxInterval := *interval
	sc.Lock()
	defer sc.Unlock()
	for _, targetGroup := range sc.C.Targets {
		packetSize := targetGroup.Size
		if packetSize < 24 || packetSize > 65535 {
			return fmt.Errorf("packet size must be in the range 24-65535, but found '%d' bytes", packetSize)
		}
		if targetGroup.Interval > maxInterval {
			maxInterval = targetGroup.Interval
		}

		// Initialize DNS resolver if needed
		if targetGroup.DNSResolve && s.resolver == nil {
			s.resolver = NewTTLAwareDNSResolver(targetGroup.DNSInterval)
		}

		for _, host = range targetGroup.Hosts {
			pinger, err := s.createPinger(host, &targetGroup)
			if err != nil {
				return fmt.Errorf("failed to create pinger for %s: %v", host, err)
			}
			pingers = append(pingers, pinger)
		}
	}
	s.prepared = pingers
	s.maxInterval = maxInterval
	return nil
}

// createPinger creates a pinger with optional DNS resolution
func (s *smokePingers) createPinger(host string, targetGroup *config.TargetGroup) (*probing.Pinger, error) {
	pinger := probing.New(host)
	pinger.Interval = targetGroup.Interval
	pinger.RecordRtts = false
	pinger.SetNetwork(targetGroup.Network)
	pinger.Size = targetGroup.Size
	pinger.SetTrafficClass(targetGroup.ToS)
	pinger.Source = targetGroup.Source
	if targetGroup.Protocol == "icmp" {
		pinger.SetPrivileged(true)
	}

	if targetGroup.DNSResolve && s.resolver != nil {
		// Use TTL-aware DNS resolution
		ip, err := s.resolver.Resolve(host)
		if err != nil {
			return nil, fmt.Errorf("DNS resolution failed: %w", err)
		}

		// Set the resolved IP directly
		pinger.SetAddr(ip.String())
		logger.Info("Pinger resolved with TTL-aware DNS", "host", host, "ip_addr", ip)
	} else {
		// Use standard resolution
		err := pinger.Resolve()
		if err != nil {
			return nil, fmt.Errorf("standard resolution failed: %w", err)
		}
		logger.Info("Pinger resolved with standard DNS", "host", host, "ip_addr", pinger.IPAddr())
	}

	return pinger, nil
}

// startDNSReresolution starts a background goroutine to periodically re-resolve DNS
func (s *smokePingers) startDNSReresolution(smokepingCollector *SmokepingCollector, pingResponseSeconds *prometheus.HistogramVec) {
	if s.resolver == nil {
		return
	}

	s.g.Go(func() error {
		ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
		defer ticker.Stop()

		for range ticker.C {
			s.checkAndUpdateDNS(smokepingCollector, pingResponseSeconds)
		}
		return nil
	})
}

// checkAndUpdateDNS checks for expired DNS entries and updates pingers if needed
func (s *smokePingers) checkAndUpdateDNS(smokepingCollector *SmokepingCollector, pingResponseSeconds *prometheus.HistogramVec) {
	if s.resolver == nil || s.started == nil {
		return
	}

	sc.RLock()
	targetGroups := make([]config.TargetGroup, len(sc.C.Targets))
	copy(targetGroups, sc.C.Targets)
	sc.RUnlock()

	needsRestart := false
	newPingers := make([]*probing.Pinger, 0, len(s.started))

	for _, pinger := range s.started {
		hostname := pinger.Addr()
		updated := false

		// Check if this pinger uses DNS resolution
		for _, targetGroup := range targetGroups {
			if !targetGroup.DNSResolve {
				continue
			}

			for _, host := range targetGroup.Hosts {
				if host == hostname {
					// Check if DNS entry has expired and needs re-resolution
					if ip, err := s.resolver.Resolve(hostname); err == nil {
						currentIP := pinger.IPAddr().IP
						if !ip.Equal(currentIP) {
							logger.Info("DNS IP changed, updating pinger",
								"hostname", hostname,
								"old_ip", currentIP,
								"new_ip", ip)

							// Create new pinger with updated IP
							newPinger, err := s.createPinger(hostname, &targetGroup)
							if err != nil {
								logger.Error("Failed to create updated pinger", "hostname", hostname, "error", err)
								newPingers = append(newPingers, pinger) // Keep old pinger
							} else {
								newPingers = append(newPingers, newPinger)
								needsRestart = true
								updated = true
							}
						}
					}
					break
				}
			}
			if updated {
				break
			}
		}

		if !updated {
			newPingers = append(newPingers, pinger)
		}
	}

	if needsRestart {
		logger.Info("Restarting pingers due to DNS changes")

		// Stop current pingers
		for _, pinger := range s.started {
			pinger.Stop()
		}

		// Update started pingers
		s.started = newPingers

		// Restart pingers with splay
		splay := time.Duration(s.maxInterval.Nanoseconds() / int64(len(s.started)))
		for _, pinger := range s.started {
			logger.Debug("Restarting pinger", "address", pinger.Addr(), "ip", pinger.IPAddr())
			s.g.Go(pinger.Run)
			time.Sleep(splay)
		}

		// Update collector
		smokepingCollector.updatePingers(s.started, *pingResponseSeconds)
	}
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

func init() {
	prometheus.MustRegister(versioncollector.NewCollector("smokeping_prober"))
}

func main() {
	var (
		configFile  = kingpin.Flag("config.file", "Optional smokeping_prober configuration yaml file.").String()
		metricsPath = kingpin.Flag("web.telemetry-path", "Path under which to expose metrics.").Default("/metrics").String()
		webConfig   = kingpinflag.AddFlags(kingpin.CommandLine, ":9374")

		buckets    = kingpin.Flag("buckets", "A comma delimited list of buckets to use").Default(defaultBuckets).String()
		factor     = kingpin.Flag("native-histogram-factor", "The scaling factor for native histogram buckets").Hidden().Default("1.05").Float()
		interval   = kingpin.Flag("ping.interval", "Ping interval duration").Short('i').Default("1s").Duration()
		privileged = kingpin.Flag("privileged", "Run in privileged ICMP mode").Default("true").Bool()
		sizeBytes  = kingpin.Flag("ping.size", "Ping packet size in bytes").Short('s').Default("56").Int()
		tosField   = kingpin.Flag("ping.tos", "Ping packet ToS field").Short('O').Default("0x00").Uint8()
		hosts      = HostList(kingpin.Arg("hosts", "List of hosts to ping"))
	)

	var smokePingers smokePingers
	var smokepingCollector *SmokepingCollector

	promslogConfig := &promslog.Config{}
	flag.AddFlags(kingpin.CommandLine, promslogConfig)
	kingpin.Version(version.Print("smokeping_prober"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()
	logger = promslog.New(promslogConfig)

	logger.Info("Starting smokeping_prober", "version", version.Info())
	logger.Info("Build context", "build_context", version.BuildContext())

	if *sizeBytes < 24 || *sizeBytes > 65535 {
		logger.Error("Invalid packet size. (24-65535)", "bytes", *sizeBytes)
		os.Exit(1)
	}

	l := func(format string, a ...interface{}) {
		logger.Info(fmt.Sprintf(strings.TrimPrefix(format, "maxprocs: "), a...), "component", "automaxprocs")
	}
	if _, err := maxprocs.Set(maxprocs.Logger(l)); err != nil {
		logger.Warn("Failed to set GOMAXPROCS automatically", "component", "automaxprocs", "err", err)
	}

	if err := sc.ReloadConfig(*configFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logger.Info("ignoring missing config file", "filename", *configFile)
		} else {
			logger.Error("error loading config", "filename", err.Error())
			os.Exit(1)
		}
	}

	bucketlist, err := parseBuckets(*buckets)
	if err != nil {
		logger.Error("Failed to parse buckets", "err", err)
		os.Exit(1)
	}
	pingResponseSeconds := newPingResponseHistogram(bucketlist, *factor)
	prometheus.MustRegister(pingResponseSeconds)

	err = smokePingers.prepare(hosts, interval, privileged, sizeBytes, tosField)
	if err != nil {
		logger.Error("Unable to create ping", "err", err)
		os.Exit(1)
	}

	if smokePingers.sizeOfPrepared() == 0 {
		logger.Error("no targets specified on command line or in config file")
		os.Exit(1)
	}

	smokePingers.start()
	smokepingCollector = NewSmokepingCollector(smokePingers.started, *pingResponseSeconds)
	prometheus.MustRegister(smokepingCollector)

	// Start DNS re-resolution if enabled
	smokePingers.startDNSReresolution(smokepingCollector, pingResponseSeconds)

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	reloadCh := make(chan chan error)
	go func() {
		for {
			var errCallback func(e error)
			var successCallback func()
			select {
			case <-hup:
				errCallback = func(e error) {}
				successCallback = func() {}
			case rc := <-reloadCh:
				errCallback = func(e error) {
					rc <- e
				}
				successCallback = func() {
					rc <- nil
				}
			}
			if err := sc.ReloadConfig(*configFile); err != nil {
				logger.Error("Error reloading config", "err", err)
				errCallback(err)
				continue
			}
			err = smokePingers.prepare(hosts, interval, privileged, sizeBytes, tosField)
			if err != nil {
				logger.Error("Unable to create ping from config", "err", err)
				errCallback(err)
				continue
			}
			if smokePingers.sizeOfPrepared() == 0 {
				logger.Error("No targets specified on command line or in config file")
				errCallback(fmt.Errorf("no targets specified"))
				continue
			}

			smokePingers.start()
			smokepingCollector.updatePingers(smokePingers.started, *pingResponseSeconds)

			// Restart DNS re-resolution if enabled
			smokePingers.startDNSReresolution(smokepingCollector, pingResponseSeconds)

			logger.Info("Reloaded config file")
			successCallback()
		}
	}()

	http.Handle(*metricsPath, promhttp.Handler())
	http.HandleFunc("/-/healthy", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Healthy"))
	})
	http.HandleFunc("/-/reload",
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				fmt.Fprintf(w, "This endpoint requires a POST request.\n")
				return
			}

			rc := make(chan error)
			reloadCh <- rc
			if err := <-rc; err != nil {
				http.Error(w, fmt.Sprintf("Failed to reload config: %s", err), http.StatusInternalServerError)
			}
		})
	if *metricsPath != "/" && *metricsPath != "" {
		landingConfig := web.LandingConfig{
			Name:        "Smokeping Prober",
			Description: "Smokeping-style packet prober for Prometheus",
			Version:     version.Info(),
			Links: []web.LandingLinks{
				{
					Address: *metricsPath,
					Text:    "Metrics",
				},
			},
		}
		landingPage, err := web.NewLandingPage(landingConfig)
		if err != nil {
			logger.Error("Failed to created landing page", "err", err)
			os.Exit(1)
		}
		http.Handle("/", landingPage)
	}

	server := &http.Server{}
	if err := web.ListenAndServe(server, webConfig, logger); err != nil {
		logger.Error("Failed to run web server", "err", err)
		os.Exit(1)
	}

	err = smokePingers.stop()
	if err != nil {
		logger.Error("Failed to run smoke pingers", "err", err)
	}

}
