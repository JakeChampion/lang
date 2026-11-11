#!/usr/bin/env bash
# Runs SCRIPT native and SCRIPT wasm at the same time. With WASM_SCRIPT,
# runs SCRIPT native and WASM_SCRIPT wasm instead. Each output line starts
# with its mode. The exit status is 1 when either mode fails. Each mode gets
# half of the CPUs for programs.sh unless FERN_IMAGE_JOBS sets the count.
set -uo pipefail
if [ $# -lt 1 ] || [ $# -gt 2 ]; then
  echo "usage: both-modes.sh SCRIPT [WASM_SCRIPT]" >&2
  exit 2
fi
if [ -z "${FERN_IMAGE_JOBS:-}" ]; then
  FERN_IMAGE_JOBS=$(( $(getconf _NPROCESSORS_ONLN) / 2 ))
  [ "$FERN_IMAGE_JOBS" -ge 1 ] || FERN_IMAGE_JOBS=1
fi
export FERN_IMAGE_JOBS
modes=(native wasm)
scripts=("$1" "${2:-$1}")
pids=()
for i in "${!modes[@]}"; do
  (set -o pipefail; "${scripts[$i]}" "${modes[$i]}" 2>&1 | sed -u "s/^/[${modes[$i]}] /") &
  pids+=($!)
done
status=0
for i in "${!modes[@]}"; do
  if ! wait "${pids[$i]}"; then
    echo "both-modes: ${scripts[$i]} ${modes[$i]} failed" >&2
    status=1
  fi
done
exit "$status"
