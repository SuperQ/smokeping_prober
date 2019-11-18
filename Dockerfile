ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:latest
LABEL maintainer="Ben Kochie <superq@gmail.com>"

ARG ARCH="amd64"
ARG OS="linux"
COPY .build/${OS}-${ARCH}/smokeping_prober /bin/smokeping_prober

EXPOSE 9374
ENTRYPOINT  [ "/bin/smokeping_prober" ]
