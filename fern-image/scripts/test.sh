#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FERN=${FERN:-fern}
mkdir -p build
"$FERN" -version
for source in experiments/essentials.fern experiments/io.fern experiments/zlib.fern experiments/provider_factories.fern tests/pixels_test.fern tests/color_test.fern tests/resize_test.fern tests/png_test.fern tests/jpeg_header_test.fern tests/png_rows_test.fern tests/png_provider_test.fern ../tests/stdlib/deflate_reader_test.fern tests/pipeline_test.fern tests/pipeline_bilinear_test.fern tests/providers_test.fern tests/regions_test.fern tests/row_cache_test.fern tests/cached_regions_test.fern tests/cached_tile_fusion_test.fern tests/linux_rss_test.fern tests/nearest_fusion_test.fern tests/cached_fusion_test.fern experiments/png-encode.fern examples/pixels.fern; do
  output="build/$(basename "$source" .fern)"
  "$FERN" -check "$source"
  "$FERN" -target x86-64-linux -sanitize -o "$output" "$source"
  "$output"
done
"$FERN" -check tests/png_incremental_test.fern
"$FERN" -target x86-64-linux -sanitize -o build/png_incremental_test tests/png_incremental_test.fern
./build/png_incremental_test
"$FERN" -check benchmarks/memory_probe.fern
"$FERN" -target x86-64-linux -sanitize -o build/memory-probe benchmarks/memory_probe.fern
for mode in unique shared retained rows rows-retained rows-cancelled eager eager-retained; do
  ./build/memory-probe "$mode" 16
  ./build/memory-probe "$mode" 128
done
"$FERN" -check benchmarks/row_cache.fern
"$FERN" -target x86-64-linux -sanitize -o build/row-cache benchmarks/row_cache.fern
for cache_bytes in 0 12000; do
  ./build/row-cache "$cache_bytes" 16
  ./build/row-cache "$cache_bytes" 128
done
"$FERN" -check benchmarks/rss_probe.fern
"$FERN" -target x86-64-linux -sanitize -o build/rss-probe benchmarks/rss_probe.fern
for mode in unique shared retained eager eager-retained rows rows-retained tiles tiles-retained; do
  ./build/rss-probe "$mode" 8
done
"$FERN" -check benchmarks/nearest_fusion.fern
"$FERN" -target x86-64-linux -sanitize -o build/nearest-fusion benchmarks/nearest_fusion.fern
for count in 8 32; do
  ./build/nearest-fusion "$count"
done
"$FERN" -check benchmarks/cached_fusion.fern
"$FERN" -target x86-64-linux -sanitize -o build/cached-fusion benchmarks/cached_fusion.fern
for mode in legacy fused; do
  for cache_bytes in 0 12000; do
    ./build/cached-fusion "$mode" "$cache_bytes" 8
  done
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
