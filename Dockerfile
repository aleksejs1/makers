# syntax=docker/dockerfile:1

# Targets:
#   dev    – hot-reload development server (air), mount the source at /src
#   test   – go vet + go test with the race detector
#   lint   – golangci-lint
#   build  – static binary
#   prod   – minimal runtime image (default)

ARG GO_VERSION=1.27
ARG GOLANGCI_LINT_VERSION=v2.15

# ---------------------------------------------------------------- base
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS base
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# ---------------------------------------------------------------- dev
FROM golang:${GO_VERSION} AS dev
WORKDIR /src
RUN go install github.com/air-verse/air@v1.67.4
ENV MAKERS_ENV=dev \
    MAKERS_PORT=8080 \
    MAKERS_DB_PATH=/data/makers.db
VOLUME /data
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]

# ---------------------------------------------------------------- test
FROM base AS test
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go vet ./... && go test -race -count=1 ./...

# ---------------------------------------------------------------- lint
FROM golangci/golangci-lint:${GOLANGCI_LINT_VERSION} AS lint
WORKDIR /src
COPY . .
RUN --mount=type=cache,target=/root/.cache golangci-lint run ./...

# ---------------------------------------------------------------- build
FROM base AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/makers ./cmd/makers
# An empty, writable data directory for the non-root user.
RUN mkdir -p /out/data && chown 65532:65532 /out/data

# ---------------------------------------------------------------- prod
FROM scratch AS prod
ARG VERSION=dev
LABEL org.opencontainers.image.title="makers.lv" \
      org.opencontainers.image.description="Community directory of projects built by makers in Latvia" \
      org.opencontainers.image.source="https://github.com/aleksejs1/makers" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/makers /makers
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
ENV MAKERS_PORT=8080 \
    MAKERS_DB_PATH=/data/makers.db
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/makers", "healthcheck"]
ENTRYPOINT ["/makers"]
CMD ["serve"]
