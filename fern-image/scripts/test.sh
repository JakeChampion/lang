#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FERN=${FERN:-fern}
mkdir -p build
"$FERN" -version
./scripts/programs.sh native \
  tests/jxl_tone_metadata_test.fern \
  tests/jxl_f16_fields_test.fern \
  tests/jxl_sample_depth_test.fern \
  tests/jxl_headers_test.fern \
  tests/jxl_integers_test.fern \
  tests/jxl_bits_test.fern \
  tests/jxl_boxes_test.fern \
  tests/png_adam7_geometry_test.fern \
  tests/png_adam7_test.fern \
  tests/qoi_test.fern \
  tests/png_grayscale_test.fern \
  tests/png_packed_test.fern \
  tests/png_palette_test.fern \
  tests/png_indexed_test.fern \
  tests/png_transparency_test.fern \
  tests/jpeg_dct_test.fern \
  tests/jpeg_quantization_test.fern \
  tests/jpeg_entropy_writer_test.fern \
  tests/jpeg_frame_writer_test.fern \
  tests/jpeg_component_blocks_test.fern \
  tests/jpeg_plane_coefficients_test.fern \
  tests/jpeg_produced_test.fern \
  tests/jpeg_mcu_order_test.fern \
  tests/jpeg_padded_blocks_test.fern \
  tests/jpeg_mcu_entropy_test.fern \
  tests/jpeg_component_frames_test.fern \
  tests/jpeg_component_planes_test.fern \
  experiments/essentials.fern \
  experiments/io.fern \
  experiments/zlib.fern \
  experiments/provider_factories.fern \
  tests/pixels_test.fern \
  tests/color_test.fern \
  tests/resize_test.fern \
  tests/png_test.fern \
  tests/jpeg_header_test.fern \
  tests/jpeg_entropy_test.fern \
  tests/jpeg_idct_test.fern \
  tests/jpeg_grayscale_test.fern \
  tests/jpeg_ycbcr_test.fern \
  tests/jpeg_rgb_ycbcr_test.fern \
  tests/sample_bytes_test.fern \
  tests/jpeg_planes_test.fern \
  tests/jpeg_chroma_test.fern \
  tests/jpeg_jfif_test.fern \
  tests/jpeg_rgb_test.fern \
  tests/png_rows_test.fern \
  tests/png_provider_test.fern \
  ../tests/stdlib/deflate_reader_test.fern \
  tests/pipeline_test.fern \
  tests/pipeline_bilinear_test.fern \
  tests/providers_test.fern \
  tests/regions_test.fern \
  tests/row_cache_test.fern \
  tests/cached_regions_test.fern \
  tests/cached_tile_fusion_test.fern \
  tests/linux_rss_test.fern \
  tests/nearest_fusion_test.fern \
  tests/cached_fusion_test.fern \
  experiments/png-encode.fern \
  examples/pixels.fern \
  tests/png_compression_test.fern \
  tests/jpeg_exif_test.fern \
  tests/jpeg_orientation_test.fern \
  tests/icc_envelope_test.fern \
  tests/icc_tag_data_test.fern \
  tests/icc_curv_eval_test.fern \
  tests/icc_curv_inverse_test.fern \
  tests/icc_power_inverse_test.fern \
  tests/icc_affine_inverse_test.fern \
  tests/icc_parametric_eval_test.fern \
  tests/icc_app2_test.fern \
  tests/icc_matrix_eval_test.fern \
  tests/icc_matrix_model_test.fern \
  tests/icc_matrix_trc_eval_test.fern \
  tests/icc_matrix_solve_test.fern \
  tests/png_incremental_test.fern \
  tests/icc_quadratic_inverse_test.fern \
  tests/icc_inverse_test.fern
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

