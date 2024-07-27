FROM golang:1.21-alpine as builder

WORKDIR /workspace
RUN apk add make
COPY ../go.mod ../go.sum ./
COPY ../staging/ ./staging
RUN go mod download
COPY .. .
RUN make cnf


FROM alpine:3.17.2
WORKDIR /cnf
RUN apk add --no-cache wireguard-tools bash wget openresolv iptables
COPY --from=builder /workspace/bin/cnf ./
ENTRYPOINT "./cnf"