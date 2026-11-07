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
  *) echo "usage: test-sample-bytes.sh native|wasm" >&2; exit 1 ;;
esac
mkdir -p build
"$FERN" -check tests/sample_bytes.fern
FERN_LEAKCHECK=1 "$FERN" -target "$target" -sanitize -o "build/sample-bytes-$mode" tests/sample_bytes.fern
"$PYTHON" tests/qualify_sample_bytes.py "build/sample-bytes-$mode" "$(command -v "$FERN")" \
  "build/sample-bytes-$mode.json" "${runner[@]}" "${@:2}"
