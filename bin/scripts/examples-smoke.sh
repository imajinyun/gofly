#!/usr/bin/env sh
set -eu

GO_CMD="${GO:-go}"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

missing_mods=""
for example in examples/*; do
	if [ -d "$example" ] && find "$example" -maxdepth 1 -name '*.go' | grep -q . && [ ! -f "$example/go.mod" ]; then
		missing_mods="${missing_mods}${example}\n"
	fi
done

if [ -n "$missing_mods" ]; then
	printf 'examples missing standalone go.mod files:\n%b' "$missing_mods" >&2
	exit 1
fi

for mod in examples/*/go.mod; do
	dir="$(dirname "$mod")"
	(cd "$dir" && "$GO_CMD" test -count=1 ./...)
	(cd "$dir" && "$GO_CMD" build -o "$workdir/$(basename "$dir")" ./...)
done

(cd examples/microshop && "$GO_CMD" run . describe) >"$workdir/microshop-topology.json"
(cd examples/ai-governed-service && "$GO_CMD" run . expected) >"$workdir/ai-governed-contract.json"

python3 - "$workdir" <<'PY'
import json
import pathlib
import sys

workdir = pathlib.Path(sys.argv[1])

with open(workdir / 'microshop-topology.json', encoding='utf-8') as f:
    topology = json.load(f)
assert len(topology) == 5, topology
assert {svc['name'] for svc in topology} >= {'gateway', 'users', 'orders', 'inventory', 'payment'}, topology

with open(workdir / 'ai-governed-contract.json', encoding='utf-8') as f:
    contract = json.load(f)
assert contract['service'] == 'ai-governed-service', contract
assert contract['adminPath'] == '/admin/control-plane', contract
PY

echo "examples smoke passed"
