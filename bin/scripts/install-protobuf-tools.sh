#!/bin/sh
# Install only the protobuf generators declared by the root module's tool directives.
set -eu

GO=${GO:-go}
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
cd "$script_dir/../.."
export GOWORK=off

module_json=$("$GO" mod edit -json)
printf '%s\n' "$module_json" | python3 -c '
import json, sys
declared = {entry["Path"] for entry in json.load(sys.stdin).get("Tool", [])}
required = {"google.golang.org/protobuf/cmd/protoc-gen-go", "google.golang.org/grpc/cmd/protoc-gen-go-grpc"}
missing = sorted(required - declared)
if missing:
    sys.exit("Missing pinned tool directives: " + ", ".join(missing))
'

tool_bin=$("$GO" env GOBIN)
if [ -z "$tool_bin" ]; then
    tool_gopath=$("$GO" env GOPATH)
    if [ -z "$tool_gopath" ]; then
        echo "protobuf tools require GOBIN or GOPATH" >&2
        exit 1
    fi
    tool_bin=${tool_gopath%%:*}/bin
fi
case "$tool_bin" in
    /*) ;;
    *) echo "protobuf tools require an absolute GOBIN or GOPATH" >&2; exit 1 ;;
esac

# A caller may supply its own fresh governance cache; clean only directories owned here.
tool_tmp=$(mktemp -d)
trap 'rm -rf "$tool_tmp"' 0
trap 'exit 130' INT
trap 'exit 143' TERM
GOCACHE=${GOCACHE:-$tool_tmp/gocache}
GOTMPDIR=${GOTMPDIR:-$tool_tmp/gotmp}
mkdir -p "$GOCACHE" "$GOTMPDIR"
export GOCACHE GOTMPDIR
export GOPROXY=direct
export GOBIN="$tool_bin"

# No @version here: go.mod tool directives and its module graph are authoritative.
"$GO" install -mod=readonly \
    google.golang.org/protobuf/cmd/protoc-gen-go \
    google.golang.org/grpc/cmd/protoc-gen-go-grpc

PATH="$tool_bin:$PATH"
export PATH
for plugin in protoc-gen-go protoc-gen-go-grpc; do
    resolved=$(command -v "$plugin")
    if [ "$resolved" != "$tool_bin/$plugin" ]; then
        echo "unexpected $plugin on PATH: $resolved" >&2
        exit 1
    fi
    printf '%s: ' "$resolved"
    "$plugin" --version
done
if [ -n "${GITHUB_PATH:-}" ]; then
    printf '%s\n' "$tool_bin" >> "$GITHUB_PATH"
fi
printf 'Protobuf tools installed in %s; add this directory to PATH for local generation.\n' "$tool_bin"
