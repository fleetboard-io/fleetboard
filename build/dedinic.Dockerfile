FROM golang:1.21 as builder

WORKDIR /workspace
RUN apt install -y  make
COPY ../go.mod ../go.sum ./
COPY ../staging ./staging
RUN go mod download
COPY .. .
RUN make dedinic


FROM  airren/kube-ovn-base:v1.13.0
WORKDIR /dedinic
COPY --from=builder /workspace/bin/dedinic  ./
COPY ../build/start-dedinic.sh .
