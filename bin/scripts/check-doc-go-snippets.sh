#!/usr/bin/env sh
set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

go_cmd="${GO:-go}"
files="${DOC_GO_SNIPPET_FILES:-}"

if [ -z "$files" ]; then
	for path in README.md README.CN.md docs/doc; do
		if [ -f "$root/$path" ]; then
			case "$path" in
			*.md)
				files="${files:+$files
}$path"
				;;
			esac
		elif [ -d "$root/$path" ]; then
			# Markdown docs are part of the repository source; paths are controlled by the project tree.
			found="$(cd "$root" && find "$path" -type f -name '*.md' | sort)"
			if [ -n "$found" ]; then
				files="${files:+$files
}$found"
			fi
		fi
	done
fi

if [ -z "$files" ]; then
	printf 'no markdown files found for Go snippet check\n'
	exit 0
fi

snippet_root="$tmp/snippets"
mkdir -p "$snippet_root"
count_file="$tmp/count"
printf '0\n' >"$count_file"

extract_file() {
	file="$1"
	base_count="$(cat "$count_file")"
	awk -v out="$snippet_root" -v count_file="$count_file" -v source="$file" -v base_count="$base_count" '
	function sanitize(value) {
		gsub(/[^A-Za-z0-9_]/, "_", value)
		return value
	}
	function start_snippet() {
		count++
		file_count++
		dir = out "/snippet_" count
		mkdir = "mkdir -p \"" dir "\""
		if (system(mkdir) != 0) {
			printf("create snippet directory failed: %s\n", dir) > "/dev/stderr"
			exit 1
		}
		go_file = dir "/" sanitize(source) "_line_" NR ".go"
		meta_file = dir "/source.txt"
		printf("%s:%d\n", source, NR) > meta_file
	}
	BEGIN {
		count = base_count + 0
		file_count = 0
		in_go = 0
	}
	/^```[[:space:]]*go([[:space:]].*)?$/ {
		if (!in_go) {
			in_go = 1
			start_snippet()
			next
		}
	}
	/^```[[:space:]]*$/ {
		if (in_go) {
			in_go = 0
			go_file = ""
			next
		}
	}
	{
		if (in_go) {
			print > go_file
		}
	}
	END {
		if (in_go) {
			printf("unterminated Go code fence in %s\n", source) > "/dev/stderr"
			exit 1
		}
		if (file_count > 0) {
			printf("%d\n", count) > count_file
		}
	}
	' "$root/$file"
}

printf '%s\n' "$files" | while IFS= read -r file; do
	[ -n "$file" ] || continue
	case "$file" in
	/*) printf 'DOC_GO_SNIPPET_FILES must use repository-relative paths: %s\n' "$file" >&2; exit 1 ;;
	esac
	if [ ! -f "$root/$file" ]; then
		printf 'markdown file not found: %s\n' "$file" >&2
		exit 1
	fi
	extract_file "$file"
done

snippet_count="$(cat "$count_file")"
if [ "$snippet_count" -eq 0 ]; then
	printf 'no Go code snippets found in markdown files\n'
	exit 0
fi

cat >"$snippet_root/go.mod" <<EOF
module gofly-doc-snippets

go 1.26

require github.com/gofly/gofly v0.0.0

replace github.com/gofly/gofly => $root
EOF

if [ -f "$root/go.sum" ]; then
	cp "$root/go.sum" "$snippet_root/go.sum"
fi

(cd "$snippet_root" && "$go_cmd" mod tidy)

printf 'checking %s Go markdown snippets\n' "$snippet_count"
if ! (cd "$snippet_root" && "$go_cmd" test ./...); then
	printf '\nGo markdown snippet compile check failed. Snippet sources:\n' >&2
	for meta in "$snippet_root"/snippet_*/source.txt; do
		[ -f "$meta" ] || continue
		printf '  %s\n' "$(cat "$meta")" >&2
	done
	exit 1
fi

printf 'Go markdown snippet compile check passed (%s snippets).\n' "$snippet_count"
