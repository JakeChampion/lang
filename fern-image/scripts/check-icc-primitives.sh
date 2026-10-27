#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FERN=${FERN:-fern}
PYTHON=${PYTHON:-python3}
mode=${1:-native}
runner=()
case "$mode" in
  native) ;;
  wasm) runner=(--runner "$(command -v wasmtime)") ;;
  arm64) runner=(--runner "$(command -v "${QEMU_AARCH64:-qemu-aarch64}")") ;;
  *) echo "usage: check-icc-primitives.sh native|wasm|arm64" >&2; exit 1 ;;
esac
"$PYTHON" tests/qualify_icc_primitives.py "$(command -v "$FERN")" "build/icc-primitives-$mode.json" --mode "$mode" "${runner[@]}"
