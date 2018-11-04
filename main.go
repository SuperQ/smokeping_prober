//

package main

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/log"
	"github.com/superq/go-ping"
	"gopkg.in/alecthomas/kingpin.v2"
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

type pingerList []ping.Pinger

func main() {
	var (
		listenAddress = kingpin.Flag("web.listen-address", "Address on which to expose metrics and web interface.").Default(":9374").String()
		metricsPath   = kingpin.Flag("web.telemetry-path", "Path under which to expose metrics.").Default("/metrics").String()

		timeout    = kingpin.Flag("ping.timeout", "Ping timeout duration").Short('t').Default("60s").Duration()
		interval   = kingpin.Flag("ping.interval", "Ping interval duration").Short('i').Default("1s").Duration()
		privileged = kingpin.Flag("privileged", "Run in privileged ICMP mode").Default("true").Bool()
		hosts      = HostList(kingpin.Arg("hosts", "List of hosts to ping").Required())
	)

	log.AddFlags(kingpin.CommandLine)
	// kingpin.Version(version.Print("smokeping_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	pingers := make([]*ping.Pinger, len(*hosts))
	for i, host := range *hosts {
		pinger, err := ping.NewPinger(host)
		if err != nil {
			fmt.Printf("ERROR: %s\n", err.Error())
			return
		}

		pinger.Interval = *interval
		pinger.Timeout = *timeout
		pinger.SetPrivileged(*privileged)

		go pinger.Run()

		pingers[i] = pinger
	}

	prometheus.MustRegister(NewSmokepingCollector(&pingers))

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
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
	for _, pinger := range pingers {
		pinger.Stop()
	}
}
