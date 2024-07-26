FROM golang:1.21-alpine as builder

WORKDIR /workspace
RUN apk update && apk add --no-cache make git
COPY ../go.mod ../go.sum ./
COPY ../staging ./staging
RUN go mod download
COPY .. .
RUN make cnf


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
    vim

WORKDIR /cnf
COPY --from=builder /workspace/bin/cnf ./
