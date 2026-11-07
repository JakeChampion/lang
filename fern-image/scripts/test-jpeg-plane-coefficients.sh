#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FERN=${FERN:-fern}
CC=${CC:-cc}
UV=${UV:-uv}
mode=${1:-native}
runner=()
case "$mode" in
  native) target=x86-64-linux ;;
  darwin) target=arm64-darwin ;;
  wasm) target=wasm32-wasi; runner=(--wasmtime "$(command -v wasmtime)") ;;
  *) echo "usage: test-jpeg-plane-coefficients.sh native|darwin|wasm" >&2; exit 1 ;;
esac
mkdir -p build
read -r -a jpeg_flags <<<"$(pkg-config --cflags --libs libjpeg)"
library=${JPEG_REFERENCE_LIBRARY:-$(pkg-config --variable=libdir libjpeg)/libjpeg.so}
"$CC" -Wall -Wextra -Werror tests/reference_jpeg_coefficients.c "${jpeg_flags[@]}" -o "build/jpeg-plane-oracle-$mode"
"$FERN" -check tests/jpeg_plane_coefficients.fern
"$FERN" -target "$target" -sanitize -o "build/jpeg-plane-coefficients-$mode" tests/jpeg_plane_coefficients.fern
"$UV" run --no-project python tests/qualify_jpeg_plane_coefficients.py \
  "build/jpeg-plane-coefficients-$mode" "$(command -v "$FERN")" "build/jpeg-plane-oracle-$mode" \
  "build/jpeg-plane-coefficients-$mode.json" --reference-library "$library" "${runner[@]}"
