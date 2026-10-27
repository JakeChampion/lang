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
  *) echo "usage: test-icc-tag-data.sh native|wasm|arm64" >&2; exit 1 ;;
esac
mkdir -p build
"$FERN" -check tests/icc_tag_data.fern
"$FERN" -target "$target" -sanitize -o "build/icc-tag-data-$mode" tests/icc_tag_data.fern
"$PYTHON" tests/qualify_icc_tag_data.py "build/icc-tag-data-$mode" "$(command -v "$FERN")" \
  "build/icc-tag-data-$mode.json" "${runner[@]}"
