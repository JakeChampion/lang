#!/usr/bin/env bash
#
# deadcode gate
# -------------
# Fails when `golang.org/x/tools/cmd/deadcode` reports an unreachable
# function that is NOT listed in tools/deadcode-allowlist.txt.
#
# Each finding is normalised to a stable "<pkg-dir>:<func-name>" key
# (line/column stripped) so the allowlist survives ordinary line-number
# churn — only the set of dead functions matters, not where they sit.
#
# Run locally with `make deadcode`. The allowlist documents the handful
# of intentionally-retained unreachable symbols (interface methods that
# are only ever dispatched dynamically, plus a few exported building
# blocks); everything else must be deleted rather than allowlisted.
set -euo pipefail

cd "$(dirname "$0")/.."

ALLOW="tools/deadcode-allowlist.txt"
DEADCODE_VERSION="v0.45.0"

raw="$(go run "golang.org/x/tools/cmd/deadcode@${DEADCODE_VERSION}" -test ./... 2>/dev/null || true)"

# "internal/lexer/lexer.go:168:17: unreachable func: Error.setFile"
#   -> "internal/lexer:Error.setFile"
found="$(printf '%s\n' "$raw" \
  | sed -nE 's#^(.*)/[^/]+\.go:[0-9]+:[0-9]+: unreachable func: (.*)$#\1:\2#p' \
  | sort -u)"

# Allowlist: drop blank lines, full-line comments, and trailing "# why" notes.
allow="$(sed -E 's/[[:space:]]*#.*$//' "$ALLOW" 2>/dev/null | grep -vE '^[[:space:]]*$' | sort -u || true)"

newdead="$(comm -23 <(printf '%s\n' "$found") <(printf '%s\n' "$allow") || true)"

if [ -n "$newdead" ]; then
  echo "deadcode gate: FAIL — unreachable function(s) not in $ALLOW:"
  printf '%s\n' "$newdead" | sed 's/^/  /'
  echo
  echo "Delete the dead code, or — if it is intentionally retained —"
  echo "add its key above to $ALLOW with a comment explaining why."
  exit 1
fi

stale="$(comm -13 <(printf '%s\n' "$found") <(printf '%s\n' "$allow") || true)"
if [ -n "$stale" ]; then
  echo "deadcode gate: OK (note — these $ALLOW entries are no longer dead"
  echo "and can be removed from the allowlist):"
  printf '%s\n' "$stale" | sed 's/^/  /'
  exit 0
fi

echo "deadcode gate: OK"
