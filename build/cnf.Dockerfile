FROM golang:1.21 as builder

WORKDIR /workspace
RUN apt install -y  make
COPY ../go.mod ../go.sum ./
COPY ../staging ./staging
RUN go mod download
COPY .. .
RUN make cnf


FROM  ubuntu:jammy
RUN apt update &&  apt install iproute2 bridge-utils tcpdump -y
RUN apt install  wireguard-tools wget openresolv iptables -y
RUN apt-get autoclean; rm -rf /var/lib/apt/lists/*
WORKDIR /cnf
COPY --from=builder /workspace/bin/cnf ./
