#!/bin/sh
set -eu

GOSKY_JWT_SECRET="${GOSKY_JWT_SECRET:-gosky-local-development-secret-at-least-32-bytes}" \
  sh ./bin/scripts/go-test.sh -shuffle=on ./internal/smoke -run '^TestGeneratedProductionServiceSmoke$'
