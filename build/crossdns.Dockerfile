FROM golang:1.21-alpine AS builder

WORKDIR /workspace
RUN apk add make
COPY ../go.mod ../go.sum ./
COPY ../staging/ ./staging
RUN go mod download
COPY .. .
RUN make crossdns


FROM scratch

COPY --from=builder /workspace/bin/crossdns  /
EXPOSE 53 53/udp
ENTRYPOINT ["/crossdns"]