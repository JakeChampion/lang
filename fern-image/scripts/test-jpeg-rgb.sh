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
  *) echo "usage: test-jpeg-rgb.sh native|wasm|arm64" >&2; exit 1 ;;
esac
if [ -n "${JPEG_RGB_RAW_ORACLE:-}" ]; then
  runner+=(--raw-oracle "$JPEG_RGB_RAW_ORACLE")
fi
if [ -n "${JPEG_RGB_ORACLE:-}" ]; then
  runner+=(--rgb-oracle "$JPEG_RGB_ORACLE")
fi
if [ "${JPEG_RGB_ALLOW_DIRTY:-0}" = 1 ]; then
  runner+=(--allow-dirty)
fi
mkdir -p build
"$FERN" -check tests/jpeg_rgb.fern
"$FERN" -target "$target" -sanitize -o "build/jpeg-rgb-$mode" tests/jpeg_rgb.fern
"$PYTHON" tests/qualify_jpeg_rgb.py "build/jpeg-rgb-$mode" "$(command -v "$FERN")" \
  "build/jpeg-rgb-$mode.json" "${runner[@]}"
