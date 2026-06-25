#!/usr/bin/env sh
set -eu

GO_CMD="${GO:-go}"
root="$(pwd)"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

for mod in examples/*/go.mod; do
	dir="$(dirname "$mod")"
	name="$(basename "$dir")"
	copy="$workdir/$name"
	cp -R "$dir" "$copy"
	while IFS= read -r sibling; do
		[ -n "$sibling" ] || continue
		src="examples/$sibling"
		dst="$workdir/$sibling"
		if [ -d "$src" ] && [ ! -e "$dst" ]; then
			cp -R "$src" "$dst"
		fi
	done <<EOF
$(sed -n 's/.*=> \.\.\/\([^[:space:]]*\).*/\1/p' "$copy/go.mod")
EOF
	(
		cd "$copy"
		"$GO_CMD" mod edit -replace "github.com/imajinyun/gofly=$root"
		"$GO_CMD" test -count=1 ./...
		"$GO_CMD" build -o "$workdir/$name.bin" ./...
	)
done

echo "examples copyable check passed"
