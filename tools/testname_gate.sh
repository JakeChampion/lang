#!/usr/bin/env bash
# Fail when a workflow selects a Go test by a name nothing in the tree
# answers to.
#
# `go test -run` treats an unmatched name as "select nothing", silently and
# with exit 0, so a lane keeps reporting green while covering less than its own
# `-run` list says it does. That is how `test-e2e-arm64`'s cross-host job spent
# its life running 15 of the 17 tests it named — including the arm64 stage-2
# fixpoint its 55-minute budget was sized for, deleted with the AST emitters in
# #5972 and never removed from the regex (#6310).
#
# Two strengths of check, because .github/ uses names two ways:
#
#   EXACT   a name terminated by `$`, `|` or `)` — the `^(A|B|C)$` and `^A$`
#           forms, plus the `ISOLATED_DRIVER_TESTS` alternation — and every
#           entry in selfhost-test-weights.txt, whose lookup is exact. These
#           must BE a test function; being a prefix of one is not enough
#           (deleting `TestFoo` while `TestFooBar` lives would otherwise pass).
#   PREFIX  everything else, i.e. deliberate prefix filters like
#           `-run '^TestArm64'`. These need only match at least one test.
set -euo pipefail

cd "$(dirname "$0")/.."

names=$(mktemp)
exact=$(mktemp)
prefix=$(mktemp)
trap 'rm -f "$names" "$exact" "$prefix"' EXIT

grep -rhoE '^func (Test[A-Za-z0-9_]*)\(' --include='*_test.go' . |
	sed -E 's/^func (Test[A-Za-z0-9_]*)\(/\1/' | sort -u >"$names"

if [ ! -s "$names" ]; then
	echo "testname gate: found no test functions at all — refusing to pass vacuously" >&2
	exit 1
fi

# Whole-line comments are prose and may name a test that was legitimately
# retired; only the machine-read parts of these files select anything.
yaml=$(grep -hvE '^[[:space:]]*#' .github/workflows/*.yml)
weights=$(grep -hvE '^[[:space:]]*#' .github/selfhost-test-weights.txt)

{
	printf '%s\n' "$yaml" | grep -oE '\bTest[A-Za-z0-9_]+[$|)]' | sed -E 's/.$//'
	printf '%s\n' "$weights" | grep -oE '^Test[A-Za-z0-9_]+'
} | sort -u >"$exact"

printf '%s\n' "$yaml" | grep -oE '\bTest[A-Za-z0-9_]+' | sort -u |
	grep -vxF -f "$exact" >"$prefix" || true

status=0
while read -r n; do
	[ -n "$n" ] || continue
	grep -qxF "$n" "$names" && continue
	echo "testname gate: '$n' is selected exactly in .github/ but is not a test function" >&2
	status=1
done <"$exact"

while read -r n; do
	[ -n "$n" ] || continue
	grep -qE "^${n}" "$names" && continue
	echo "testname gate: '$n' is used as a test-name prefix in .github/ but matches no test" >&2
	status=1
done <"$prefix"

if [ "$status" -ne 0 ]; then
	echo "" >&2
	echo "A -run name that matches nothing selects nothing, and go test says so with" >&2
	echo "exit 0. Point it at the current name, or delete it and re-size the lane's" >&2
	echo "timeout to what it actually runs." >&2
	exit 1
fi

echo "testname gate: $(wc -l <"$exact" | tr -d ' ') exact + $(wc -l <"$prefix" | tr -d ' ') prefix test selectors in .github/ resolve"
