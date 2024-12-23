FROM golang:1.21-alpine AS builder

WORKDIR /workspace
RUN apk update && apk add --no-cache make git
COPY ../go.mod ../go.sum ./
RUN go mod download
COPY .. .
RUN make proxy


FROM alpine:latest

# Install required packages
RUN apk update && apk add --no-cache \
    iproute2 \
    bridge-utils \
    tcpdump \
    iputils \
    wireguard-tools \
    wget \
    openresolv \
    iptables \
    vim \
    ipvsadm \
    ipset

WORKDIR /proxy
COPY --from=builder /workspace/bin/proxy ./
