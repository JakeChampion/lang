#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FERN=${FERN:-fern}
mode=${1:-native}
runner=()
case "$mode" in
  native) target=x86-64-linux ;;
  wasm) target=wasm32-wasi; runner=(--wasmtime "$(command -v wasmtime)") ;;
  *) echo "usage: test-jpeg-quantization.sh native|wasm" >&2; exit 1 ;;
esac
mkdir -p build
"$FERN" -check tests/jpeg_quantization.fern
"$FERN" -target "$target" -sanitize -o "build/jpeg-quantization-$mode" tests/jpeg_quantization.fern
uv run --no-project python tests/qualify_jpeg_quantization.py "build/jpeg-quantization-$mode" "$(command -v "$FERN")" \
  "build/jpeg-quantization-$mode.json" "${runner[@]}"
