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
  *) echo "usage: test-jpeg-frame-writer.sh native|darwin|wasm" >&2; exit 1 ;;
esac
read -r -a jpeg_flags <<<"$(pkg-config --cflags --libs libjpeg)"
library=${JPEG_REFERENCE_LIBRARY:-$(pkg-config --variable=libdir libjpeg)/libjpeg.so}
mkdir -p build
"$CC" -Wall -Wextra -Werror tests/reference_jpeg_coefficients.c "${jpeg_flags[@]}" -o "build/jpeg-frame-oracle-$mode"
"$FERN" -check tests/jpeg_frame_writer.fern
"$FERN" -target "$target" -sanitize -o "build/jpeg-frame-writer-$mode" tests/jpeg_frame_writer.fern
"$UV" run --no-project python tests/qualify_jpeg_frame_writer.py \
  "build/jpeg-frame-writer-$mode" "$(command -v "$FERN")" "build/jpeg-frame-oracle-$mode" \
  "build/jpeg-frame-writer-$mode.json" --reference-library "$library" "${runner[@]}"
