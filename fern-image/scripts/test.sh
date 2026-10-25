#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FERN=${FERN:-fern}
mkdir -p build
"$FERN" -version
for source in experiments/essentials.fern experiments/io.fern experiments/zlib.fern tests/pixels_test.fern tests/color_test.fern tests/resize_test.fern tests/png_test.fern tests/pipeline_test.fern tests/pipeline_bilinear_test.fern experiments/png-encode.fern examples/pixels.fern; do
  output="build/$(basename "$source" .fern)"
  "$FERN" -check "$source"
  "$FERN" -target x86-64-linux -sanitize -o "$output" "$source"
  "$output"
done
"$FERN" -check benchmarks/memory_probe.fern
"$FERN" -target x86-64-linux -sanitize -o build/memory-probe benchmarks/memory_probe.fern
for mode in unique shared retained rows rows-retained rows-cancelled; do
  ./build/memory-probe "$mode" 16
  ./build/memory-probe "$mode" 128
done
for spec in immutable-array:E056 immutable-field:E048 usize-checked:E009; do
  name=${spec%:*}
  code=${spec#*:}
  cp "experiments/$name.fern.txt" "build/$name.fern"
  if "$FERN" -check "build/$name.fern" > "build/$name.log" 2>&1; then
    echo "Expected compiler rejection: $name" >&2
    exit 1
  fi
  grep -qF "$code" "build/$name.log"
  echo "Expected compiler rejection passed: $name ($code)"
done

"$FERN" -fmt experiments/format-match.fern > build/format-match.fern
"$FERN" -target x86-64-linux -sanitize -o build/format-match build/format-match.fern
./build/format-match
"$FERN" -fmt build/format-match.fern > build/format-match-again.fern
cmp build/format-match.fern build/format-match-again.fern
echo "Formatter match regression passed"
