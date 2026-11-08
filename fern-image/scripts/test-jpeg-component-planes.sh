#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FERN=${FERN:-fern}
CC=${CC:-cc}
UV=${UV:-uv}
mode=${1:-native}
case "$mode" in
  native)
    case "$(uname -s)/$(uname -m)" in
      Darwin/arm64) target=arm64-darwin ;;
      Linux/aarch64|Linux/arm64) target=arm64-linux ;;
      Linux/x86_64) target=x86-64-linux ;;
      *) echo "unsupported native host" >&2; exit 1 ;;
    esac ;;
  wasm) target=wasm32-wasi ;;
  *) echo "usage: test-jpeg-component-planes.sh native|wasm" >&2; exit 1 ;;
esac
mkdir -p build
read -r -a jpeg_flags <<<"$(pkg-config --cflags --libs libjpeg)"
library=${JPEG_REFERENCE_LIBRARY:-$(pkg-config --variable=libdir libjpeg)/libjpeg.so}
"$CC" -Wall -Wextra -Werror tests/reference_jpeg_scan.c "${jpeg_flags[@]}" -o "build/jpeg-component-plane-oracle-$mode"
"$FERN" -check tests/jpeg_component_planes.fern
FERN_LEAKCHECK=1 "$FERN" -target "$target" -sanitize -o "build/jpeg-component-planes-$mode" tests/jpeg_component_planes.fern
arguments=("build/jpeg-component-planes-$mode" "$(command -v "$FERN")" "build/jpeg-component-plane-oracle-$mode"
  "build/jpeg-component-planes-$mode.json" --reference-library "$library")
if [ "$mode" = wasm ]; then
  arguments+=(--wasmtime "$(command -v wasmtime)")
fi
"$UV" run --no-project python tests/qualify_jpeg_component_planes.py "${arguments[@]}" "${@:2}"
