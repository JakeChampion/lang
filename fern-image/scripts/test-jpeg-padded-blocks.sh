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
  *) echo "usage: test-jpeg-padded-blocks.sh native|wasm" >&2; exit 1 ;;
esac
mkdir -p build
"$FERN" -check tests/jpeg_padded_blocks.fern
FERN_LEAKCHECK=1 "$FERN" -target "$target" -sanitize -o "build/jpeg-padded-blocks-$mode" tests/jpeg_padded_blocks.fern
"$PYTHON" tests/qualify_jpeg_padded_blocks.py "build/jpeg-padded-blocks-$mode" "$(command -v "$FERN")" \
  "build/jpeg-padded-blocks-$mode.json" "${runner[@]}" "${@:2}"
