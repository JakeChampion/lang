#!/usr/bin/env bash
# bench/maps/run.sh — build both halves of the map comparison and run the
# matrix: every table at 4 000, 100 000 and 1 000 000 keys, rounds chosen so
# each cell does a few hundred thousand lookups at least. Prints the report
# lines; bench/maps/README.md holds the last recorded run.
#
# Needs bin/fern-selfhost (make build) and a Rust toolchain.
set -euo pipefail
cd "$(dirname "$0")/../.."
out="${TMPDIR:-/tmp}/fern-maps-bench"
mkdir -p "$out"
for f in map_int_lookup map_string_lookup constmap_lookup; do
  ./bin/fern-selfhost -O -target x86-64-linux -o "$out/$f" "bench/maps/fern/$f.fern" internal/stdlib
done
(cd bench/maps/rust && CARGO_NET_GIT_FETCH_WITH_CLI=true cargo build --release --quiet)
for cell in "4000 40" "100000 10" "1000000 2"; do
  set -- $cell
  echo "## n=$1 rounds=$2"
  bench/maps/rust/target/release/fern-maps-comparison "$1" "$2" 0.9
  "$out/map_int_lookup" "$1" "$2"
  "$out/map_string_lookup" "$1" "$2"
  "$out/constmap_lookup" "$1" "$2"
done
