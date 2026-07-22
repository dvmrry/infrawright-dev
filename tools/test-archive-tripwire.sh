#!/usr/bin/env bash
set -euo pipefail

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd -P)
js_runtime='no''de'
package_runtime='np''m'
package_exec='np''x'

scratch=$(mktemp -d)
export_root="$scratch/export"
intercept="$scratch/intercept"
mkdir -p "$export_root" "$intercept"
trap 'chmod -R u+w "$scratch" 2>/dev/null || true; rm -rf -- "$scratch"' EXIT

# Materialize the tracked/current source tree without Git metadata, ignored
# build output, or user-owned reports. NUL delimiters preserve path safety.
while IFS= read -r -d '' path; do
	case "$path" in
		reports/*) continue ;;
	esac
	if [[ -e "$repo_root/$path" || -L "$repo_root/$path" ]]; then
		mkdir -p "$export_root/$(dirname -- "$path")"
		cp -pP "$repo_root/$path" "$export_root/$path"
	fi
done < <(git -C "$repo_root" ls-files --cached --others --exclude-standard -z)

make -C "$export_root" archive-tripwire

expect_reject_append() {
	local relative=$1
	local content=$2
	local label=$3
	local backup="$scratch/backup"
	cp -p "$export_root/$relative" "$backup"
	printf '\n%s\n' "$content" >> "$export_root/$relative"
	if make -C "$export_root" archive-tripwire >"$scratch/reject.out" 2>&1; then
		printf 'archive-tripwire regression: accepted %s\n' "$label" >&2
		exit 1
	fi
	cp -p "$backup" "$export_root/$relative"
}

# Ordinary prose at command-like punctuation boundaries must remain valid.
{
	printf '\n(Node runtime references are historical.)\n'
	printf 'The document discusses (%s behavior) only for provenance.\n' "$js_runtime"
	printf '\140%s command\140 is a historical term.\n' "$js_runtime"
	printf '%s runtime references are historical.\n' "$js_runtime"
} >> "$export_root/README.md"
printf '\nEach Virtual Service Edge %s must be registered.\n' "$js_runtime" >> \
	"$export_root/packs/_shared/zscaler/demo/README.md"
make -C "$export_root" archive-tripwire

expect_reject_append README.md \
	"${js_runtime} deleted-runtime.mjs" 'root README command'
expect_reject_append Makefile \
	$'archive-bypass:\n\tenv '"${js_runtime}"' build.mjs' 'environment-wrapped Make command'
expect_reject_append Makefile \
	$'archive-bypass:\n\tsh -c '\'' '"${js_runtime}"' build.mjs'\''' 'shell-wrapped Make command'
expect_reject_append .github/workflows/check.yml \
	$'\n# run: "'"${js_runtime}"' build.mjs"' 'quoted workflow command'
expect_reject_append .github/workflows/check.yml \
	$'\n# run: env '"${js_runtime}"' build.mjs' 'environment-wrapped workflow command'
expect_reject_append .github/workflows/check.yml \
	$'\n# run: /usr/bin/'"${js_runtime}"' build.mjs' 'path-qualified workflow command'
expect_reject_append .github/workflows/check.yml \
	$'\n# run: PATH=/usr/bin '"${js_runtime}"' build.mjs' 'PATH-assigned workflow command'
expect_reject_append .github/workflows/check.yml \
	$'\n# shell: '"${js_runtime}"' {0}' 'custom runtime workflow shell'
expect_reject_append .github/workflows/check.yml \
	$'\n# run: '"${package_runtime}"' ci' 'package-manager workflow command'
expect_reject_append .github/workflows/check.yml \
	$'\n# run: '"${package_exec}"' esbuild entry.ts' 'package-exec workflow command'

mkdir -p "$export_root/nested/runtime"
printf '{}\n' > "$export_root/nested/runtime/package.json"
if make -C "$export_root" archive-tripwire >"$scratch/reject.out" 2>&1; then
	printf 'archive-tripwire regression: accepted nested package manifest\n' >&2
	exit 1
fi
rm -f -- "$export_root/nested/runtime/package.json"
rmdir "$export_root/nested/runtime" "$export_root/nested"

mkdir -p "$export_root/scripts"
printf '// archived runtime bypass\n' > "$export_root/scripts/build.mjs"
if make -C "$export_root" archive-tripwire >"$scratch/reject.out" 2>&1; then
	printf 'archive-tripwire regression: accepted runtime source\n' >&2
	exit 1
fi
rm -f -- "$export_root/scripts/build.mjs"
rmdir "$export_root/scripts"

printf '#!/usr/bin/env %s\n' "$js_runtime" > "$export_root/runtime-tool"
chmod +x "$export_root/runtime-tool"
if make -C "$export_root" archive-tripwire >"$scratch/reject.out" 2>&1; then
	printf 'archive-tripwire regression: accepted runtime shebang\n' >&2
	exit 1
fi
rm -f -- "$export_root/runtime-tool"

mkdir -p "$export_root/tools/dir with spaces"
printf '%s deleted-runtime.mjs\n' "$js_runtime" > \
	"$export_root/tools/dir with spaces/README.md"
if make -C "$export_root" archive-tripwire >"$scratch/reject.out" 2>&1; then
	printf 'archive-tripwire regression: accepted command in path with spaces\n' >&2
	exit 1
fi
rm -f -- "$export_root/tools/dir with spaces/README.md"
rmdir "$export_root/tools/dir with spaces"

# Install the failing runtime shims before the build. -B makes the complete
# gate rebuild dist/iw even when a local artifact already exists.
for runtime in "$js_runtime" "$package_runtime" "$package_exec"; do
	printf '#!/bin/sh\nexit 99\n' > "$intercept/$runtime"
	chmod +x "$intercept/$runtime"
done
PATH="$intercept:$PATH" CI=true make -B -C "$repo_root" check check-root-catalog
