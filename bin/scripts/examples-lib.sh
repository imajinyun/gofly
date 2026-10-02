#!/usr/bin/env sh

examples_catalog_paths() {
	repository_root="$1"
	python3 - "$repository_root/examples/catalog.json" <<'PY'
import json
import pathlib
import sys

catalog_path = pathlib.Path(sys.argv[1])
catalog = json.loads(catalog_path.read_text(encoding="utf-8"))
for item in catalog.get("examples") or []:
    path = item.get("path")
    if isinstance(path, str):
        print(path)
PY
}

examples_prepare_go_env() {
	cache_root="$1"
	mkdir -p "$cache_root/gocache" "$cache_root/gotmp"
	export GOCACHE="$cache_root/gocache"
	export GOTMPDIR="$cache_root/gotmp"
	case " ${GOFLAGS:-} " in
	*" -count=1 "*) ;;
	*) export GOFLAGS="${GOFLAGS:+$GOFLAGS }-count=1" ;;
	esac
	if [ "${GOVERNANCE_ISOLATE_GOMODCACHE:-false}" = "true" ]; then
		mkdir -p "$cache_root/gomodcache"
		export GOMODCACHE="$cache_root/gomodcache"
	fi
}

examples_build_module() {
	go_cmd="$1"
	output="$2"
	main_packages="$("$go_cmd" list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./...)"
	if [ -n "$(printf '%s\n' "$main_packages" | sed '/^[[:space:]]*$/d')" ]; then
		mkdir -p "$output"
		"$go_cmd" build -o "$output/" ./...
		return
	fi
	"$go_cmd" build ./...
}
