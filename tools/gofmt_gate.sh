#!/usr/bin/env bash
#
# gofmt gate
# ----------
# Reports the Go files in this repository that gofmt would rewrite, and
# with --fix rewrites them.
#
# The file set comes from git, not from walking the directory. `gofmt -l .`
# descends into everything .gitignore covers as well, and this repository
# keeps agent worktrees under .claude/ — separate checkouts with their own
# in-progress edits. A half-finished edit in one of those made `make
# gofmt-check` fail in the main checkout, which blocks an unrelated push
# and names files the pusher does not own. CI never saw it because a fresh
# clone has no worktrees, so it only bit local runs.
#
# Tracked plus untracked-but-not-ignored is the set that is actually this
# repository's to format: a new file counts before it is staged, an ignored
# one never does.
set -euo pipefail

fix=0
if [ "${1:-}" = "--fix" ]; then
  fix=1
elif [ $# -gt 0 ]; then
  echo "usage: $0 [--fix]" >&2
  exit 2
fi

# Index entries whose file is gone (a deletion not yet committed) would make
# gofmt exit non-zero on a missing path, so drop them.
files=()
while IFS= read -r -d '' f; do
  [ -f "$f" ] && files+=("$f")
done < <(git ls-files -z --cached --others --exclude-standard -- '*.go')

if [ ${#files[@]} -eq 0 ]; then
  exit 0
fi

out=$(gofmt -l "${files[@]}")

if [ -z "$out" ]; then
  exit 0
fi

if [ "$fix" -eq 1 ]; then
  # shellcheck disable=SC2086
  gofmt -w $out
  echo "reformatted:"
  echo "$out"
  exit 0
fi

echo "not gofmt-clean:"
echo "$out"
# shellcheck disable=SC2086
gofmt -d $out
exit 1
