FROM quay.io/prometheus/busybox:glibc
LABEL maintainer="Ben Kochie <superq@gmail.com>"

COPY smokeping_prober /bin/smokeping_prober

EXPOSE 9374
ENTRYPOINT  [ "/bin/smokeping_prober" ]
