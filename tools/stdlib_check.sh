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

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

status=0
checked=0
while IFS= read -r f; do
	mod=${f#internal/stdlib/}
	mod=${mod%.fern}
	case "$(basename "$mod")" in
	_test*) continue ;; # fixtures with nothing to check
	esac
	printf 'import "%s";\nfunction main(): i32 { return 0; }\n' "$mod" >"$tmp/main.fern"
	if ! ./bin/fern -check "$tmp/main.fern"; then
		echo "FAIL: \"$mod\" does not type-check as a standalone import" >&2
		status=1
	fi
	checked=$((checked + 1))
done < <(find internal/stdlib -name '*.fern' | sort)

if [ "$checked" -eq 0 ]; then
	echo "stdlib check: found no stdlib modules at all — refusing to pass vacuously" >&2
	exit 1
fi

exit $status
