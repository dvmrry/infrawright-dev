#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$repo_root"

# Keep the executable names assembled at run time so this scanner and its
# regression harness are subject to the same content scan as every other shell
# script. There is no self-exemption in the active script surface.
js_runtime='no''de'
package_runtime='np''m'
package_exec='np''x'
runtime_names="${js_runtime}|${package_runtime}|${package_exec}"

# Executable surfaces fail closed on any direct, wrapped, quoted, assigned, or
# path-qualified runtime token. Current workflow documents use the narrower
# runnable-argument pattern below so ordinary domain prose remains valid.
command_pattern="(^|[[:space:]\"'=:;&|()\`])([^[:space:]\"'=:;&|()\`]*/)?(${runtime_names})([[:space:]\"'/{;}]|$)"
legacy_pattern='actions/setup-'"${js_runtime}"'|dist/infrawright-cli\.mjs|INFRAWRIGHT_'\
'CLI|IW_'\
'MAINTAINER|IW_'\
'OPERATOR|\$\((NODE|NPM)\)'
script_argument='([^[:space:]\"'"'"'`]+\.(js|mjs|cjs|jsx|ts|tsx)|[-./][^[:space:]\"'"'"'`]*|\{0\})'
package_action='(ci|install|run|test|exec|pack|publish|start|stop|restart|version|view|audit|outdated|update|uninstall|link|login|logout|whoami|config|cache|init|create|help)'
document_pattern="(^|[[:space:]\"'=:;&|()\`])([^[:space:]\"'=:;&|()\`]*/)?(${js_runtime})[[:space:]]+[\"'\`]*${script_argument}|(^|[[:space:]\"'=:;&|()\`])([^[:space:]\"'=:;&|()\`]*/)?(${package_runtime})[[:space:]]+${package_action}([[:space:]\"'\`]|$)|(^|[[:space:]\"'=:;&|()\`])([^[:space:]\"'=:;&|()\`]*/)?(${package_exec})[[:space:]]+[\"'\`]*(esbuild|tsx|ts-node)([[:space:]\"'\`]|$)"
shebang_pattern="^#!.*(^|[/[:space:]])(${runtime_names})([[:space:]]|$)"

fail_with_matches() {
	description=$1
	matches=$2
	if test -n "$matches"; then
		printf '%s\n' "$matches" >&2
		printf 'archive-tripwire: %s\n' "$description" >&2
		exit 1
	fi
}

# Workspace metadata, user-owned reports, caches, and generated distribution
# output are not shipped source. Everything else is an active-tree candidate.
residue=$(find . \
	\( -path './.git' -o -path './.claude' -o -path './.pytest_cache' -o -path './reports' -o -path './dist' \) -prune -o \
	\( -type d \( -name node_modules -o -name node-src -o -name .node-test \) -o \
	-type f \( -name package.json -o -name package-lock.json -o -name npm-shrinkwrap.json -o -name yarn.lock -o -name pnpm-lock.yaml -o -name .node-version -o -name .nvmrc \) \) -print)
fail_with_matches 'package-manager residue remains in the active tree' "$residue"

# These two TypeScript files are frozen provenance: they identify the exact
# pre-archive source probes that produced committed Go test authorities. They
# are not executable from the current tree; their recovery instructions point
# to the immutable oracle tag.
runtime_sources=$(find . \
	\( -path './.git' -o -path './.claude' -o -path './.pytest_cache' -o -path './reports' -o -path './dist' \) -prune -o \
	-type f \( -name '*.js' -o -name '*.mjs' -o -name '*.cjs' -o -name '*.jsx' -o -name '*.ts' -o -name '*.tsx' \) \
	! -path './go/internal/roots/testdata/probe/topology_probe.ts' \
	! -path './go/internal/roots/testdata/probe/scope_plan_probe.ts' -print)
fail_with_matches 'runtime source remains outside the two frozen provenance probes' "$runtime_sources"

executable_matches=$(
	{
		find . \
			\( -path './.git' -o -path './.claude' -o -path './.pytest_cache' -o -path './reports' -o -path './dist' \) -prune -o \
			-type f \( -name 'Makefile' -o -name '*.mk' -o -name '*.sh' -o -name '*.bash' -o -name '*.zsh' \) \
			-exec grep -nHE "${legacy_pattern}|${command_pattern}" {} +
		find .github/workflows -type f \( -name '*.yml' -o -name '*.yaml' \) \
			-exec grep -nHE "${legacy_pattern}|${command_pattern}" {} +
	} 2>/dev/null || :
)
fail_with_matches 'executable runtime reference remains in Make, CI, or a shell script' "$executable_matches"

shebang_matches=$(find . \
	\( -path './.git' -o -path './.claude' -o -path './.pytest_cache' -o -path './reports' -o -path './dist' \) -prune -o \
	-type f -perm -111 -exec awk -v pattern="$shebang_pattern" \
		'FNR == 1 && $0 ~ pattern { print FILENAME ":" FNR ":" $0 }' {} +)
fail_with_matches 'runtime executable remains in an active shebang' "$shebang_matches"

# Scan current operator/maintainer documentation, including the root README,
# all tool/pack READMEs, operational/provider-lab docs, and recipe files. The
# historical archive, roadmap, and review records intentionally retain recovery
# commands and are not current workflow documents.
document_matches=$(
	{
		find README.md go/README.md docs/operational-runtime.md -type f -exec grep -nHE "$document_pattern" {} +
		find tools packs docs/provider-labs docs/recipes -type f \
			\( -name 'README.md' -o -name '*.md' -o -name '*.json' -o -name '*.yaml' -o -name '*.yml' \) \
			-exec grep -nHE "$document_pattern" {} +
	} 2>/dev/null || :
)
fail_with_matches 'executable runtime command remains in a current workflow document' "$document_matches"
