#!/usr/bin/env sh
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
go_cmd="${GO:-go}"
testflags="${TESTFLAGS:--shuffle=on}"
tmp="$(mktemp -d)"
trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT INT TERM

case " ${GOFLAGS:-} " in
*" -count=1 "*) ;;
*) export GOFLAGS="${GOFLAGS:+$GOFLAGS }-count=1" ;;
esac
export GOCACHE="${GOCACHE:-$tmp/gocache}"
export GOTMPDIR="${GOTMPDIR:-$tmp/gotmp}"
mkdir -p "$GOCACHE" "$GOTMPDIR"

sh "$root/bin/scripts/check-dependency-upgrade-evidence.sh"
sh "$root/bin/scripts/check-root-dependency-policy.sh"
sh "$root/bin/scripts/check-mod-tidy.sh"

GOFLY_CACHE_DISABLED=true "$go_cmd" test $testflags ./cache -run 'Test(CacheDisabledBy|TieredCacheDisabledBy)'
"$go_cmd" test $testflags ./cmd/gofly/internal/generator -run 'TestPluginRunnerDownloadPlugin(DoesNotReuseLocalCache|IgnoresUserCache|UsesUniqueTempFile)'

printf '%s\n' "cache and remote-dependency governance ok"
