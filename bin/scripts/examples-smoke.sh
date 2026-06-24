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
(cd examples/cache-local && "$GO_CMD" run .) >"$workdir/cache-local.json"
(cd examples/rpc-idl-matrix && "$GO_CMD" run .) >"$workdir/rpc-idl-matrix.json"
(cd examples/plugin-ecosystem && "$GO_CMD" run .) >"$workdir/plugin-ecosystem.json"

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

with open(workdir / 'cache-local.json', encoding='utf-8') as f:
    cache_local = json.load(f)
assert cache_local['schema'] == 'gofly.cache_local.v1', cache_local
assert {'typed-local-cache', 'loader-fill', 'negative-cache', 'bloom-protection', 'tiered-l1-l2-cache', 'cache-disabled-mode', 'stats-and-prometheus'} <= set(cache_local['capabilities']), cache_local
assert cache_local['local']['loaderCalls'] == 1, cache_local
assert cache_local['local']['stats']['loads'] == 1, cache_local
assert cache_local['local']['stats']['hits'] == 1, cache_local
assert cache_local['negative']['loaderCalls'] == 1, cache_local
assert cache_local['negative']['stats']['negatives'] == 1, cache_local
assert cache_local['bloom']['stats']['bloomRejects'] == 1, cache_local
assert cache_local['tiered']['loaderCalls'] == 1, cache_local
assert cache_local['tiered']['namespacedRemote'] is True, cache_local
assert cache_local['disabled']['loaderCalls'] == 2, cache_local
assert cache_local['disabled']['stats']['disabled'] is True, cache_local

with open(workdir / 'rpc-idl-matrix.json', encoding='utf-8') as f:
    rpc_matrix = json.load(f)
assert rpc_matrix['schema'] == 'gofly.rpc_idl_matrix.v1', rpc_matrix
assert rpc_matrix['idl']['proto'] == 'contracts/greeter.proto', rpc_matrix
assert rpc_matrix['idl']['thrift'] == 'contracts/greeter.thrift', rpc_matrix
assert {item['mode'] for item in rpc_matrix['streams']} >= {'unary', 'server_stream', 'client_stream', 'bidi_stream'}, rpc_matrix
assert set(rpc_matrix['balancers']) >= {'round_robin', 'weighted_round_robin', 'p2c', 'consistent_hash', 'health_aware'}, rpc_matrix
assert {'recovery', 'trace', 'logging', 'timeout', 'retry', 'breaker', 'validation'} <= set(rpc_matrix['interceptors']['unary']), rpc_matrix
assert {'recovery', 'trace', 'logging', 'timeout', 'breaker'} <= set(rpc_matrix['interceptors']['stream']), rpc_matrix
assert rpc_matrix['results']['retryAttempts'] == '2', rpc_matrix
assert rpc_matrix['results']['unary'] == 'hello gofly', rpc_matrix

with open(workdir / 'plugin-ecosystem.json', encoding='utf-8') as f:
    plugin_matrix = json.load(f)
assert plugin_matrix['schema'] == 'gofly.plugin_ecosystem.v1', plugin_matrix
assert plugin_matrix['protocol'] == '1', plugin_matrix
assert plugin_matrix['registry']['path'] == 'registry/plugins.json', plugin_matrix
assert {'audit-trail-generator', 'company-template-pack'} <= set(plugin_matrix['registry']['names']), plugin_matrix
assert {'name', 'version', 'protocol', 'compatibleVersions', 'capabilities', 'permissions', 'checksum', 'source'} <= set(plugin_matrix['registry']['fields']), plugin_matrix
compatibility = {item['name']: item['accepted'] for item in plugin_matrix['compatibility']}
assert compatibility == {'old-protocol': False, 'current-protocol': True, 'future-plus-current': True, 'future-only': False}, plugin_matrix
examples = {item['name']: item for item in plugin_matrix['examples']}
assert 'internal/audit/audit.go' in examples['example-file-generator']['files'], plugin_matrix
assert 'cmd/orders/main.go' in examples['example-patch-generator']['patches'], plugin_matrix
assert examples['third-party-template-directory']['contract'] == 'templates/service/gofly.template.json', plugin_matrix
PY

echo "examples smoke passed"
