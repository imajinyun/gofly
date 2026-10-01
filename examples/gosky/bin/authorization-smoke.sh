#!/bin/sh
set -eu

GOSKY_JWT_SECRET="${GOSKY_JWT_SECRET:-gosky-local-development-secret-at-least-32-bytes}" \
  go test -count=1 ./internal/smoke -run '^TestGeneratedProductionServiceSmoke$'
