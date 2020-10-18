# smokeping_prober
Prometheus style "smokeping" prober.

![Example Graph](example-graph.png)

## Overview

This prober sends a series of ICMP (or UDP) pings to a target and records the responses in Prometheus histogram metrics.

```
usage: smokeping_prober [<flags>] <hosts>...

Flags:
  -h, --help              Show context-sensitive help (also try --help-long and --help-man).
      --web.listen-address=":9374"
                          Address on which to expose metrics and web interface.
      --web.telemetry-path="/metrics"  
                          Path under which to expose metrics.
      --buckets=BUCKETS   A comma delimited list of buckets to use
  -i, --ping.interval=1s  Ping interval duration
      --privileged        Run in privileged ICMP mode
      --log.level="info"  Only log messages with the given severity or above. Valid levels: [debug, info, warn,
                          error, fatal]
      --log.format="logger:stderr"  
                          Set the log target and format. Example: "logger:syslog?appname=bob&local=7" or
                          "logger:stdout?json=true"
      --version           Show application version.

Args:
  <hosts>  List of hosts to ping
```

## Building and running

Requires Go >= 1.11

```console
go get github.com/superq/smokeping_prober
sudo setcap cap_net_raw=+ep ${GOPATH}/bin/smokeping_prober
```

## Metrics

 Metric Name                            | Type       | Description
----------------------------------------|------------|-------------------------------------------
 smokeping\_requests\_total             | Counter    | Counter of pings sent.
 smokeping\_responses\_total            | Counter    | Counter of responses received.
 smokeping\_response\_duration\_seconds | Histogram  | Ping response duration.
