# syntax=docker/dockerfile:1.6

FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY bte ./bte
COPY sbc ./sbc
COPY bloc-node ./bloc-node

WORKDIR /src/bloc-node
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/bloc-node ./cmd/bloc-node

FROM debian:bookworm-slim AS runtime

RUN useradd --system --uid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin bloc
COPY --from=build /out/bloc-node /usr/local/bin/bloc-node

USER 10001:10001
WORKDIR /work
EXPOSE 8000 9000

ENTRYPOINT ["bloc-node"]
CMD ["run", "--config", "/config/cluster.json"]
