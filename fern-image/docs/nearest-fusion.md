# Uncached nearest-row fusion

Uncached row execution composes consecutive nearest-neighbor coordinate maps without intermediate row images.
It preserves each checked integer mapping and its rounding step.
It does not replace a chain with a direct resize between its endpoint dimensions.

For example, a four-pixel row resized to three pixels and then two selects original indices zero and three.
A direct four-to-two resize selects indices one and three.
The independent RGB/RGBA golden checks this difference in both dimensions and preserves transparent RGB bytes.

## Selection and resource admission

Let W be the final row width, and let D be the number of consecutive nearest nodes.
The ordinary path evaluates sum(w_i) horizontal nearest coordinates across those nodes.
The fused path evaluates W times D.
Fusion requires more than one nearest node and W times D no greater than sum(w_i).
Otherwise, execution retains the ordinary path.

This condition keeps horizontal coordinate evaluations within the existing sample estimate.
Vertical mapping still applies every intermediate step once per row.
`required_samples`, `required_scratch`, and public admission checks keep their existing conservative values.
Fusion does not admit a previously rejected graph.

The chain ends at a generated source, materialized source, or bilinear node.
Bilinear nodes keep their interpolation and byte rounding before nearest coordinates select their pixels.
The graph depth limit remains 64 nodes.
All graph validation completes before execution. The validation includes nodes that fusion skips during materialization.

Cache contexts retain per-node execution and cache statistics. Zero cache payload bytes keep this behavior.
The optimization applies to uncached row requests and their use inside ordinary tile requests.
Fusion for cached requests needs a separate accounting contract.

## Cancellation and ownership

Cancellation checks occur during vertical coordinate composition, before source requests, and before every group of 64 output pixels.
Cancellation releases retained source rows and partially rendered outputs.
The output remains an owned row with the existing copy-on-write semantics.
Source images and caller-retained rows remain isolated from later updates.

Six cases verify independent coordinates, mixed-filter agreement, exact admission, upsample fallback, cancellation, depth, ownership, and cache statistics.
They pass on native x86-64, ARM64/QEMU, and Wasmtime with zero final live bytes.
All 76 image TAP cases pass through the native and Wasm scripts with compiler `8c22550998a5`.

## Allocator observations

One compiler builds both implementations from the same workload and stdlib sources.
The baseline pipeline comes from main `7f5fa78e09f4`.
The allocator record uses compiler `8c22550998a5` for both implementations.
The workload creates a virtual 1024x8192 RGBA solid source and four nearest nodes.
The final node has dimensions 128x24000 and returns 512-byte rows.
The consumer discards each checked output row.

Both paths retain a 21,504-byte logical scratch estimate and 2,688 samples per row.
Horizontal coordinate evaluations fall from 1,664 to 512 per output row in this workload.
The measurement enables the leak census without the sanitizer quarantine.
Three fresh processes repeat each configuration, with no warmup processes.

| Output rows | Baseline allocations | Fused allocations | Baseline fresh bytes | Fused fresh bytes |
| ---: | ---: | ---: | ---: | ---: |
| 8 | 313,936 | 92,544 | 13,200 | 6,080 |
| 32 | 1,255,744 | 370,176 | 13,200 | 6,080 |

The record includes compiler, binary, collector, workload, pipeline, library, and workspace stdlib hashes.
The collector checks every common baseline input and rejects changes during collection.
Both binaries verify their outputs and report zero final live bytes.
The raw observations are in [nearest-fusion-m4.json](../benchmarks/results/nearest-fusion-m4.json).

Fresh allocator demand does not measure peak live bytes, isolated allocator overhead, RSS, or a process memory upper bound.
The solid-source workload does not qualify representative color throughput.
These observations make no timing, performance-parity, CPU-deadline, or production-safety claim.

## Reproduction

Run these commands from the repository root.
Use the same absolute compiler path for both builds.

```sh
fusion_root=$PWD
fusion_compiler=$PWD/bin/fern
git worktree add --detach /tmp/fusion-before 7f5fa78e09f49440a804b139107e0a1d58e95067
cp fern-image/benchmarks/nearest_fusion.fern /tmp/fusion-before/fern-image/benchmarks/
(
  cd /tmp/fusion-before
  FERN_LEAKCHECK=1 "$fusion_compiler" -target x86-64-linux \
    -o /tmp/nearest-baseline0 fern-image/benchmarks/nearest_fusion.fern
  python3 "$fusion_root/fern-image/benchmarks/measure_nearest_fusion.py" \
    record-baseline /tmp/nearest-baseline0 "$fusion_compiler" /tmp/fusion-baseline.json
)
FERN_LEAKCHECK=1 "$fusion_compiler" -target x86-64-linux \
  -o /tmp/nearest-candidate fern-image/benchmarks/nearest_fusion.fern
python3 fern-image/benchmarks/measure_nearest_fusion.py \
  /tmp/nearest-baseline0 /tmp/nearest-candidate "$fusion_compiler" \
  /tmp/fusion-baseline.json /tmp/nearest-fusion.json
```
