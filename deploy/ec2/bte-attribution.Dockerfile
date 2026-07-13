# syntax=docker/dockerfile:1.6

FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY bte ./bte

WORKDIR /src/bte/btd-impl-main
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/bte-attribution ./cmd/bte-attribution
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go test -c -o /out/paper-bench .

FROM debian:bookworm-slim AS runtime

RUN useradd --system --uid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin bloc
COPY --from=build /out/bte-attribution /usr/local/bin/bte-attribution
COPY --from=build /out/paper-bench /usr/local/bin/paper-bench

USER 10001:10001
WORKDIR /work
ENTRYPOINT ["bte-attribution"]
CMD ["run", "--help"]
