FROM golang as builder

WORKDIR /go/src/github.com/superq/smokeping_prober
COPY . .
RUN make build

FROM quay.io/prometheus/busybox:glibc
LABEL maintainer="Ben Kochie <superq@gmail.com>"

COPY --from=builder /go/src/github.com/superq/smokeping_prober/smokeping_prober /bin/smokeping_prober

EXPOSE 9374
ENTRYPOINT  [ "/bin/smokeping_prober" ]
