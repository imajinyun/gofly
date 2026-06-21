#!/usr/bin/env sh
set -eu

GO_CMD="${GO:-go}"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

"$GO_CMD" test -count=1 ./examples/...
"$GO_CMD" build ./examples/...
"$GO_CMD" run ./examples/microshop describe >"$workdir/microshop-topology.json"
"$GO_CMD" run ./examples/ai-governed-service expected >"$workdir/ai-governed-contract.json"

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
