#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FERN=${FERN:-fern}
PYTHON=${PYTHON:-python3}
mode=${1:-native}
runner=()
case "$mode" in
  native) target=x86-64-linux ;;
  wasm) target=wasm32-wasi; runner=(--wasmtime "$(command -v wasmtime)") ;;
  arm64) target=arm64-linux; runner=(--qemu "$(command -v "${QEMU_AARCH64:-qemu-aarch64}")") ;;
  *) echo "usage: test-icc-matrix-eval.sh native|wasm|arm64" >&2; exit 1 ;;
esac
mkdir -p build
"$FERN" -check tests/icc_matrix_eval.fern
"$FERN" -target "$target" -sanitize -o "build/icc-matrix-eval-$mode" tests/icc_matrix_eval.fern
"$PYTHON" tests/qualify_icc_matrix_eval.py "build/icc-matrix-eval-$mode" "$(command -v "$FERN")" \
  "build/icc-matrix-eval-$mode.json" "${runner[@]}"
