#!/usr/bin/env bash
# Runs SCRIPT native and SCRIPT wasm at the same time. Each output line starts
# with its mode. The exit status is 1 when either mode fails.
set -uo pipefail
if [ $# -lt 1 ]; then
  echo "usage: both-modes.sh SCRIPT [ARG...]" >&2
  exit 2
fi
script=$1
shift
modes=(native wasm)
pids=()
for mode in "${modes[@]}"; do
  (set -o pipefail; "$script" "$mode" "$@" 2>&1 | sed -u "s/^/[$mode] /") &
  pids+=($!)
done
status=0
for i in "${!modes[@]}"; do
  if ! wait "${pids[$i]}"; then
    echo "both-modes: $script ${modes[$i]} failed" >&2
    status=1
  fi
done
exit "$status"
