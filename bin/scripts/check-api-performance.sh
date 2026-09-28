#!/usr/bin/env sh
set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
cd "$root"

exec python3 "$root/bin/scripts/check-api-performance.py" "$@"
