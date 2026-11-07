#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FERN=${FERN:-fern}
PYTHON=${PYTHON:-python3}
mode=${1:-native}
runner=()
case "$mode" in
  native)
    case "$(uname -s)/$(uname -m)" in
      Darwin/arm64) target=arm64-darwin ;;
      Linux/aarch64|Linux/arm64) target=arm64-linux ;;
      Linux/x86_64) target=x86-64-linux ;;
      *) echo "unsupported native host" >&2; exit 1 ;;
    esac ;;
  wasm) target=wasm32-wasi; runner=(--wasmtime "$(command -v wasmtime)") ;;
  arm64) target=arm64-linux; runner=(--qemu "$(command -v "${QEMU_AARCH64:-qemu-aarch64}")") ;;
  *) echo "usage: test-jpeg-rgb-ycbcr.sh native|wasm|arm64" >&2; exit 1 ;;
esac
mkdir -p build
"$FERN" -check tests/jpeg_rgb_ycbcr.fern
FERN_LEAKCHECK=1 "$FERN" -target "$target" -sanitize -o "build/jpeg-rgb-ycbcr-$mode" tests/jpeg_rgb_ycbcr.fern
"$PYTHON" tests/qualify_jpeg_rgb_ycbcr.py "build/jpeg-rgb-ycbcr-$mode" "$(command -v "$FERN")" \
  "build/jpeg-rgb-ycbcr-$mode.json" "${runner[@]}" "${@:2}"
