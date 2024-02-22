FROM golang:1.21-alpine as builder

WORKDIR /workspace
RUN apk add make
COPY ../go.mod ../go.sum ./
COPY ../staging/ ./staging
RUN go mod download
COPY .. .
RUN make ep-controller


FROM alpine:3.17.2

COPY --from=builder /workspace/bin/ep-controller  /
ENTRYPOINT "/ep-controller"