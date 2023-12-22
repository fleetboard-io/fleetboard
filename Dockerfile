FROM alpine:3.7 as ovnmaster
WORKDIR /
COPY ./cmd/ovnmaster .
ENTRYPOINT ["./ovnmaster"]