#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FERN=${FERN:-fern}
mkdir -p build
"$FERN" -version
wasmtime --version
for source in experiments/essentials.fern experiments/zlib.fern experiments/provider_factories.fern tests/pixels_test.fern tests/color_test.fern tests/resize_test.fern tests/png_test.fern tests/jpeg_header_test.fern tests/jpeg_entropy_test.fern tests/jpeg_idct_test.fern tests/jpeg_grayscale_test.fern tests/jpeg_ycbcr_test.fern tests/jpeg_planes_test.fern tests/jpeg_chroma_test.fern tests/jpeg_jfif_test.fern tests/jpeg_rgb_test.fern tests/png_rows_test.fern tests/png_provider_test.fern ../tests/stdlib/deflate_reader_test.fern tests/pipeline_test.fern tests/pipeline_bilinear_test.fern tests/providers_test.fern tests/regions_test.fern tests/row_cache_test.fern tests/cached_regions_test.fern tests/cached_tile_fusion_test.fern tests/linux_rss_test.fern tests/nearest_fusion_test.fern tests/cached_fusion_test.fern; do
  output="build/wasm-$(basename "$source" .fern).wasm"
  "$FERN" -check "$source"
  "$FERN" -target wasm32-wasi -sanitize -o "$output" "$source"
  wasmtime "$output"
done
"$FERN" -check tests/jpeg_exif_test.fern
"$FERN" -target wasm32-wasi -sanitize -o build/wasm-jpeg_exif_test.wasm tests/jpeg_exif_test.fern
wasmtime build/wasm-jpeg_exif_test.wasm
"$FERN" -check tests/jpeg_orientation_test.fern
"$FERN" -target wasm32-wasi -sanitize -o build/wasm-jpeg_orientation_test.wasm tests/jpeg_orientation_test.fern
wasmtime build/wasm-jpeg_orientation_test.wasm
"$FERN" -check tests/icc_envelope_test.fern
"$FERN" -target wasm32-wasi -sanitize -o build/wasm-icc_envelope_test.wasm tests/icc_envelope_test.fern
wasmtime run --dir=. build/wasm-icc_envelope_test.wasm
"$FERN" -check tests/icc_tag_data_test.fern
"$FERN" -target wasm32-wasi -sanitize -o build/wasm-icc_tag_data_test.wasm tests/icc_tag_data_test.fern
wasmtime run --dir=. build/wasm-icc_tag_data_test.wasm
"$FERN" -check tests/png_incremental_test.fern
"$FERN" -target wasm32-wasi -sanitize -o build/wasm-png_incremental_test.wasm tests/png_incremental_test.fern
wasmtime build/wasm-png_incremental_test.wasm
"$FERN" -check benchmarks/nearest_fusion.fern
"$FERN" -target wasm32-wasi -sanitize -o build/wasm-nearest-fusion.wasm benchmarks/nearest_fusion.fern
wasmtime build/wasm-nearest-fusion.wasm 8
"$FERN" -check benchmarks/memory_probe.fern
"$FERN" -target wasm32-wasi -sanitize -o build/wasm-memory-probe.wasm benchmarks/memory_probe.fern
for mode in unique shared retained rows rows-retained rows-cancelled eager eager-retained; do
  wasmtime build/wasm-memory-probe.wasm "$mode" 16
done
"$FERN" -check benchmarks/row_cache.fern
"$FERN" -target wasm32-wasi -sanitize -o build/wasm-row-cache.wasm benchmarks/row_cache.fern
for cache_bytes in 0 12000; do
  wasmtime build/wasm-row-cache.wasm "$cache_bytes" 16
done
"$FERN" -check benchmarks/cached_fusion.fern
"$FERN" -target wasm32-wasi -sanitize -o build/wasm-cached-fusion.wasm benchmarks/cached_fusion.fern
for mode in legacy fused; do
  for cache_bytes in 0 12000; do
    wasmtime build/wasm-cached-fusion.wasm "$mode" "$cache_bytes" 8
  done
done
