FROM alpine:3.7 as ovnmaster
WORKDIR /
COPY ./cmd/ovnmaster/ovnmaster .
ENTRYPOINT ["./ovnmaster"]


FROM  alpine:3.7 as syncer
WORKDIR /
COPY ./cmd/syncer/syncer .
ENTRYPOINT ["./syncer"]

