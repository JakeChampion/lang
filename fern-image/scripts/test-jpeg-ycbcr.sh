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
  *) echo "usage: test-jpeg-ycbcr.sh native|wasm|arm64" >&2; exit 1 ;;
esac
mkdir -p build
"$FERN" -check tests/jpeg_ycbcr.fern
"$FERN" -target "$target" -sanitize -o "build/jpeg-ycbcr-$mode" tests/jpeg_ycbcr.fern
"$PYTHON" tests/qualify_jpeg_ycbcr.py "build/jpeg-ycbcr-$mode" "$(command -v "$FERN")" \
  "build/jpeg-ycbcr-$mode.json" "${runner[@]}"
