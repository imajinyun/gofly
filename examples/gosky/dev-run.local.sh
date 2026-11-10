#!/usr/bin/env bash
# Local-only helper: start the gosky backend against the local MySQL dev
# database. Run from examples/gosky in a dedicated terminal.
# Prereqs: migrations applied + seed + policy bootstrap (see dev-reset.local.sh).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"

export GOCACHE="${GOCACHE:-/tmp/gocache}"
export GOTMPDIR="${GOTMPDIR:-/tmp/gotmp}"
export GOFLAGS="${GOFLAGS:--mod=mod}"
export GOSKY_MYSQL_DSN="${GOSKY_MYSQL_DSN:-root:bytedance@tcp(127.0.0.1:3306)/gosky?parseTime=true&charset=utf8mb4}"
export GOSKY_JWT_SECRET="${GOSKY_JWT_SECRET:-gosky-local-development-secret-at-least-32-bytes}"
export GOSKY_FILE_ROOT="${GOSKY_FILE_ROOT:-/tmp/gosky-files}"
mkdir -p "$GOCACHE" "$GOTMPDIR" "$GOSKY_FILE_ROOT"

exec go run ./cmd/gosky
