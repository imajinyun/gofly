# syntax=docker/dockerfile:1.7
#
# Multi-stage build for the gofly CLI binary.
#   docker build -t gofly:latest .
#   docker run --rm gofly:latest version
#
# Targets:
#   builder  - compiles the CLI; re-usable for downstream projects
#   runtime  - distroless image containing only the binary
#   debug    - debian-based image with a shell for troubleshooting

ARG BUILDER_IMAGE=golang:1.26-alpine@sha256:f1ddd9fe14fffc091dd98cb4bfa999f32c5fc77d2f2305ea9f0e2595c5437c14
ARG BASE_IMAGE=gcr.io/distroless/static-debian12:nonroot@sha256:d093aa3e30dbadd3efe1310db061a14da60299baff8450a17fe0ccc514a16639
ARG DEBUG_IMAGE=gcr.io/distroless/base-debian12:debug-nonroot@sha256:ddd86b705dac25b3cc5f9d580018c6397c6b02ae5c2fa58ae95409c71e73cc3b

# ---- builder -----------------------------------------------------------------
FROM ${BUILDER_IMAGE} AS builder

WORKDIR /src

# Leverage build cache: install ca-certificates and pre-fetch deps.
RUN apk add --no-cache ca-certificates git tzdata && \
    update-ca-certificates 2>/dev/null || true

ENV CGO_ENABLED=0 \
    GO111MODULE=on \
    GOTOOLCHAIN=local

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Build arguments for reproducible builds; override at CI time.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILT_AT=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    LDFLAGS="-s -w \
             -X 'github.com/gofly/gofly/cmd/gofly/internal/command.Version=${VERSION}' \
             -X 'github.com/gofly/gofly/cmd/gofly/internal/command.Commit=${COMMIT}' \
             -X 'github.com/gofly/gofly/cmd/gofly/internal/command.BuiltAt=${BUILT_AT}'" && \
    go build -trimpath -ldflags "${LDFLAGS}" -o /out/gofly ./cmd/gofly

# ---- runtime -----------------------------------------------------------------
FROM ${BASE_IMAGE} AS runtime

WORKDIR /

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /out/gofly /usr/local/bin/gofly

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/gofly"]
CMD ["--help"]

# ---- debug -------------------------------------------------------------------
FROM ${DEBUG_IMAGE} AS debug

WORKDIR /

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /out/gofly /usr/local/bin/gofly

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/gofly"]
CMD ["--help"]
