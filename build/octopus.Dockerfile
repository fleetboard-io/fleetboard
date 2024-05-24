FROM golang:1.21-alpine as builder

WORKDIR /workspace
RUN apk add make
COPY ../go.mod ../go.sum ./
COPY ../staging/ ./staging
RUN go mod download
COPY .. .
RUN make octopus


FROM alpine:3.17.2
RUN apk add --no-cache wireguard-tools bash wget openresolv iptables
COPY --from=builder /workspace/bin/octopus  /
ENTRYPOINT "/octopus"