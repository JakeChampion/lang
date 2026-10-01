#!/usr/bin/env bash
# Type-check every stdlib module as a standalone import, with the native
# `fern -check`.
#
# Import-wrapper form because a std module is not an entry module: checking
# e.g. std/array directly redeclares the `__method_*` builtins it defines.
# Per-module (rather than one program importing everything) so a module that
# leans on another module's functions without declaring the `import` fails
# here instead of on the first program that imports it alone — the same
# property TestStdlibModulesImportStandalone pins via the Go API.
set -euo pipefail

cd "$(dirname "$0")/.."

if [ ! -x bin/fern ]; then
	echo "stdlib check: bin/fern missing — run via \`make check-sources\`" >&2
	exit 1
fi

# One wrapper program per module, each in its own directory, checked in
# parallel: the checks are independent and each one is a process.
check_module() {
	f=$1
	mod=${f#internal/stdlib/}
	mod=${mod%.fern}
	dir=$(mktemp -d)
	trap 'rm -rf "$dir"' EXIT
	printf 'import "%s";\nfunction main(): i32 { return 0; }\n' "$mod" >"$dir/main.fern"
	if ! ./bin/fern -check "$dir/main.fern"; then
		echo "FAIL: \"$mod\" does not type-check as a standalone import" >&2
		exit 1
	fi
}
export -f check_module

mapfile -t modules < <(find internal/stdlib -name '*.fern' | sort | grep -v '/_test[^/]*\.fern$')
checked=${#modules[@]}
status=0
if [ "$checked" -gt 0 ]; then
	printf '%s\n' "${modules[@]}" |
		xargs -P "$(nproc)" -n 1 bash -c 'check_module "$0"' || status=1
fi

if [ "$checked" -eq 0 ]; then
	echo "stdlib check: found no stdlib modules at all — refusing to pass vacuously" >&2
	exit 1
fi

exit $status
