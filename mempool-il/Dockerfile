FROM golang:1.25-alpine AS build

WORKDIR /src

COPY bte/btd-impl-main /src/bte/btd-impl-main
COPY mempool-il /src/mempool-il

WORKDIR /src/mempool-il
RUN go build -o /out/mempool-il ./cmd/service

FROM alpine:3.22

RUN adduser -D -H -u 10001 mempool
COPY --from=build /out/mempool-il /usr/local/bin/mempool-il
USER mempool
EXPOSE 8080

ENTRYPOINT ["mempool-il"]
