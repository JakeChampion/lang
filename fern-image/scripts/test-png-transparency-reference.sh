#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p build
libdir=$(pkg-config --variable=libdir libpng)
library="$libdir/libpng16.so"
if [[ -f "$libdir/libpng16.dylib" ]]; then
  library="$libdir/libpng16.dylib"
fi
# libpng is an offline reference. No Fern runtime links to this executable.
read -r -a flags <<< "$(pkg-config --cflags --libs libpng)"
"${CC:-cc}" -std=c11 -Wall -Wextra -Werror tests/png_reference.c "${flags[@]}" -o build/png-reference
"${UV:-uv}" run --no-project "${PYTHON:-python3}" tests/qualify_png_transparency.py \
  build/png-reference build/png-transparency-reference.json --reference-library "$library"
