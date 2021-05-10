# smokeping_prober
Prometheus style "smokeping" prober.

![Example Graph](example-graph.png)

## Overview

This prober sends a series of ICMP (or UDP) pings to a target and records the responses in Prometheus histogram metrics.

```
usage: smokeping_prober [<flags>] [<hosts>...]

Flags:
  -h, --help              Show context-sensitive help (also try --help-long and --help-man).
      --config.file="smokeping_prober.yml"
                          Optional smokeping_prober configuration file.
      --web.listen-address=":9374"
                          Address on which to expose metrics and web interface.
      --web.telemetry-path="/metrics"
                          Path under which to expose metrics.
      --buckets="5e-05,0.0001,0.0002,0.0004,0.0008,0.0016,0.0032,0.0064,0.0128,0.0256,0.0512,0.1024,0.2048,0.4096,0.8192,1.6384,3.2768,6.5536,13.1072,26.2144"
                          A comma delimited list of buckets to use
  -i, --ping.interval=1s  Ping interval duration
      --privileged        Run in privileged ICMP mode
      --log.level="info"  Only log messages with the given severity or above. Valid levels: [debug, info, warn,
                          error, fatal]
      --log.format="logger:stderr"  
                          Set the log target and format. Example: "logger:syslog?appname=bob&local=7" or
                          "logger:stdout?json=true"
      --version           Show application version.

Args:
  [<hosts>]  List of hosts to ping
```

## Configuration

The prober can take a list of targets and parameters from the command line or from a yaml config file.

Example config:

```yaml
---
targets:
- hosts:
  - host1
  - host2
  interval: 1s # Duration, Default 1s.
  network: ip # One of ip, ip4, ip6. Default: ip (automatic IPv4/IPv6)
  protocol: icmp # One of icmp, udp. Default: icmp (Requires privileged operation)
```

In each host group the `interval`, `network`, and `protocol` are optional.

The interval Duration is in [Go time.ParseDuration()](https://golang.org/pkg/time/#ParseDuration) syntax.

## Building and running

Requires Go >= 1.14

```console
go get github.com/superq/smokeping_prober
sudo setcap cap_net_raw=+ep ${GOPATH}/bin/smokeping_prober
```

## Metrics

 Metric Name                            | Type       | Description
----------------------------------------|------------|-------------------------------------------
 smokeping\_requests\_total             | Counter    | Counter of pings sent.
 smokeping\_response\_duration\_seconds | Histogram  | Ping response duration.
