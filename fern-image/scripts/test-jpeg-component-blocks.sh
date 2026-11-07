#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FERN=${FERN:-fern}
mode=${1:-native}
runner=()
case "$mode" in
  native) target=x86-64-linux ;;
  darwin) target=arm64-darwin ;;
  wasm) target=wasm32-wasi; runner=(--wasmtime "$(command -v wasmtime)") ;;
  *) echo "usage: test-jpeg-component-blocks.sh native|darwin|wasm" >&2; exit 1 ;;
esac
mkdir -p build
"$FERN" -check tests/jpeg_component_blocks.fern
"$FERN" -target "$target" -sanitize -o "build/jpeg-component-blocks-$mode" tests/jpeg_component_blocks.fern
uv run --no-project python tests/qualify_jpeg_component_blocks.py \
  "build/jpeg-component-blocks-$mode" "$(command -v "$FERN")" \
  "build/jpeg-component-blocks-$mode.json" "${runner[@]}"
