FROM alpine:3.7 as ovnmaster
WORKDIR /
COPY ./cmd/ovnmaster/ovnmaster .
ENTRYPOINT ["./ovnmaster"]


FROM  alpine:3.7 as syncer
WORKDIR /
COPY ./cmd/syncer/syncer .
ENTRYPOINT ["./syncer"]

FROM  alpine:3.7 as octopus
WORKDIR /
COPY ./cmd/octopus/octopus .
ENTRYPOINT ["./octopus"]

FROM scratch as crossdns

ADD ./cmd/crossdns/crossdns /crossdns

EXPOSE 53 53/udp
ENTRYPOINT ["/crossdns"]
