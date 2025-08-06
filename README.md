# smokeping_prober

[![CircleCI](https://circleci.com/gh/SuperQ/smokeping_prober/tree/master.svg?style=svg)](https://circleci.com/gh/SuperQ/smokeping_prober/tree/master)
[![Docker Repository on Quay](https://quay.io/repository/superq/smokeping-prober/status "Docker Repository on Quay")](https://quay.io/repository/superq/smokeping-prober)

Prometheus style "smokeping" prober.

![Example Graph](example-graph.png)

## Overview

This prober sends a series of ICMP (or UDP) pings to a target and records the responses in Prometheus histogram metrics.

```
usage: smokeping_prober [<flags>] [<hosts>...]

Flags:
  -h, --help                     Show context-sensitive help (also try --help-long and --help-man).
      --config.file=CONFIG.FILE  Optional smokeping_prober configuration yaml file.
      --web.telemetry-path="/metrics"
                                 Path under which to expose metrics.
      --web.systemd-socket       Use systemd socket activation listeners instead of port listeners (Linux only).
      --web.listen-address=:9374 ...
                                 Addresses on which to expose metrics and web interface. Repeatable for multiple
                                 addresses.
      --web.config.file=""       [EXPERIMENTAL] Path to configuration file that can enable TLS or authentication.
      --buckets="5e-05,0.0001,0.0002,0.0004,0.0008,0.0016,0.0032,0.0064,0.0128,0.0256,0.0512,0.1024,0.2048,0.4096,0.8192,1.6384,3.2768,6.5536,13.1072,26.2144"
                                 A comma delimited list of buckets to use
  -i, --ping.interval=1s         Ping interval duration
      --privileged               Run in privileged ICMP mode
  -s, --ping.size=56             Ping packet size in bytes
      --log.level=info           Only log messages with the given severity or above. One of: [debug, info, warn,
                                 error]
      --log.format=logfmt        Output format of log messages. One of: [logfmt, json]
      --version                  Show application version.

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
  size: 56 # Packet data size in bytes. Default 56 (Range: 24 - 65535)
  source: 127.0.1.1 # Souce IP address to use. Default: None (automatic selection)
  dns_resolve: true # Enable TTL-aware DNS resolution. Default: false
  dns_interval: 5m # Override DNS re-resolution interval (ignores TTL). Optional
```

In each host group the `interval`, `network`, `protocol`, `dns_resolve`, and `dns_interval` are optional.

### DNS TTL-Aware Resolution

The smokeping_prober supports DNS TTL-aware resolution, which automatically re-resolves hostnames when their DNS TTL expires. This is useful for monitoring services that may change IP addresses over time.

When `dns_resolve: true` is set in a target group:
- The prober will query DNS servers directly to get TTL information
- Hostnames will be automatically re-resolved when their TTL expires
- If the IP address changes, the pinger will be updated automatically
- DNS resolution metrics are exposed for monitoring

Optional `dns_interval` can be used to override TTL-based timing with a fixed interval.

The interval Duration is in [Go time.ParseDuration()](https://golang.org/pkg/time/#ParseDuration) syntax.

The config is read on startup, and can be reloaded with the SIGHUP signal, or with an HTTP POST to the URI path `/-/reload`.

## Building and running

Requires Go >= 1.22

```console
go install github.com/superq/smokeping_prober@latest
sudo setcap cap_net_raw=+ep ${GOPATH}/bin/smokeping_prober
```

On multi-cpu systems it is typically more efficient to limit the prober to one CPU in order to
reduce the number of cross-cpu context switches and packet copies from the kernel to the prober.
This can be done with the `GOMAXPROCS` environment variable, or by using container (cgroup) limits.

```console
export GOMAXPROCS=1
./smokeping_prober <targets>
```

## Docker

```bash
docker run \
  -p 9374:9374 \
  --privileged \
  --env GOMAXPROCS=1 \
  quay.io/superq/smokeping-prober:latest \
  some-ping-target.example.com
```

## Metrics

 Metric Name                                      | Type       | Description
--------------------------------------------------|------------|-------------------------------------------
 smokeping\_requests\_total                       | Counter    | Counter of pings sent.
 smokeping\_response\_duration\_seconds           | Histogram  | Ping response duration.
 smokeping\_response\_ttl                         | Gauge      | The last response Time To Live (TTL).
 smokeping\_response\_duplicates\_total           | Counter    | The number of duplicated response packets.
 smokeping\_receive\_errors\_total                | Counter    | The number of errors when Pinger attempts to receive packets.
 smokeping\_send\_errors\_total                   | Counter    | The number of errors when Pinger attempts to send packets.
 smokeping\_prober\_dns\_resolve\_total           | Counter    | Total number of DNS resolutions attempted.
 smokeping\_prober\_dns\_resolve\_duration\_seconds | Histogram  | Time spent on DNS resolution.
 smokeping\_prober\_dns\_cache\_entries           | Gauge      | Number of entries in the DNS cache.

### TLS and basic authentication

The Smokeping Prober supports TLS and basic authentication.

To use TLS and/or basic authentication, you need to pass a configuration file
using the `--web.config.file` parameter. The format of the file is described
[in the exporter-toolkit repository](https://github.com/prometheus/exporter-toolkit/blob/master/docs/web-configuration.md).


### Health check

A health check can be requested in the URI path `/-/healthy`.
