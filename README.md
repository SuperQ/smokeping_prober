# smokeping_exporter
Prometheus style "smokeping" prober.

## Overview

This prober sends a series of ICMP (or UDP) pings to a target and records the responses in Prometheus histogram metrics.

```
usage: smokeping_exporter [<flags>] <hosts>...

Flags:
  -h, --help              Show context-sensitive help (also try --help-long and --help-man).
      --web.listen-address=":9374"
                          Address on which to expose metrics and web interface.
      --web.telemetry-path="/metrics"  
                          Path under which to expose metrics.
  -t, --ping.timeout=60s  Ping timeout duration
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

## Metrics

 Metric Name                         | Type       | Description
-------------------------------------|------------|-------------------------------------------
 smokeping\_requests\_total          | Counter    | Counter of pings sent.
 smokeping_response_duration_seconds | Histogram  | Ping response duration.
