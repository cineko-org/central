# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26.6-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
COPY vendor ./vendor

COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -mod=vendor -trimpath -ldflags "-s -w" \
      -o /out/cineko-central ./cmd/cineko-central

FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install --yes --no-install-recommends ca-certificates curl && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd --system --gid 10001 cineko && \
    useradd --system --uid 10001 --gid cineko --home-dir /var/lib/cineko-central --shell /usr/sbin/nologin cineko && \
    install -d -o cineko -g cineko -m 0700 /var/lib/cineko-central

COPY --from=builder --chown=root:root /out/cineko-central /usr/local/bin/cineko-central

USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/cineko-central"]
