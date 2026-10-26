# Aggregate view queries in the checker

Issue #13449 belongs to main allocation failure #13439.
The new aggregate escape analysis repeatedly traverses the same nominal type graph.
Allocation profiles identify these queries as the main added cost on fixed inputs.
The fix caches complete root queries within each scope family.

Scope copies share their nominal tables and cache.
Each new scope family creates a fresh cache.
Canonical type spellings distinguish concrete arguments and view qualifiers.
Unresolved spellings bypass the cache. Type variables preserve the conservative result.
Recursive visits do not enter the cache because their ancestor set can hide a later view path.

## Fixed input

All three compiler snapshots use the same emitter and embedded libraries from clean compiler `c4c31eae08f5`.
The emitter SHA-256 is `91ded1f0ec323c1a73ad3b8eea9db0044ef844d860c31136c34f6a2a80b7e13d`.
Each compiler compiles the exact checker source and stdlib from `43616e4d4cdf1221220e24efb3687d2b28e51bf9`.
This control separates compiler changes from growth of the benchmark input.

| Compiler source | Allocations | Frees |
| --- | ---: | ---: |
| Baseline `43616e4d4cdf` | 31,624,582 | 31,624,582 |
| Before `789a7a81934c` | 40,282,366 | 40,282,366 |
| After `8db514acbe99` | 34,085,292 | 34,085,292 |

Three fresh processes reproduce each row. The collector alternates compiler order.
All nine processes report zero final live bytes.
Each compiler produces identical assembly hashes across its three repetitions.
The before/after reduction is approximately 15.4% in allocation events.
The result remains above the earlier baseline compiler.

`fixed-input.json` records source commits, binary hashes, commands, and every observation.
The collector verifies fixed sources and tool hashes again after execution.
Compiler source associations are explicit arguments, not values inferred from arbitrary binaries.
These counts do not measure CPU time, peak live bytes, allocator overhead, or RSS.

## Profiles

`baseline.pprof.gz` and `current.pprof.gz` preserve allocation profiles from the same fixed input.
Their adjacent text files contain cumulative object estimates from `go tool pprof`.
The profile build adds `FERN_PROFILE=alloc-pprof` and a `FERN_PROFILE_FILE` path.
Both builds use the same emitter as the fixed-input comparison.

The profiler samples every 64th allocation and retains at most 8192 stacks.
Systematic sampling and reservoir selection can bias per-function estimates.
The current profile attributes approximately 8.58 million objects to aggregate view queries.
That estimate is diagnostic evidence. It is not an exact allocation count for those functions.
The separate leak counters supply the exact event totals above.

## Actual allocation gate

The unchanged benchmark on clean main `3d523fa00d6c` reports 40,849,794 allocations and frees.
The gate rejects its 29.17% drift against the recorded 31,624,467 events.
After integration of upstream main `f4902b7ae`, source `33d61509df79` reports 34,696,726 allocations and frees.
The clean compiler reports `33d61509df79` and has SHA-256 `8a04d1dca6541bff69fb2811361ddd18e423d07e04957f970f7e2220d865be68`.
The benchmark and the gate both exit zero.

The unchanged 10% ceiling passes. The 1% advisory tolerance still warns about 9.71% drift.

This PR changes no baseline, tolerance, exemption, or diagnostic rule.
`allocation-gate.json`, `gate-before.txt`, and `gate-after.txt` preserve the results.
The benchmark input grows between these main snapshots, so the fixed-input comparison supplies the controlled result.
This host has no delegated cgroup. The benchmark reports no peak RSS.

## Correctness checks

Four new parity cases cover recursive borrowed holders, parameter-backed holders, generic owned/view instances, and recursive owned storage.
They preserve E063 for local views and accept safe cases.
The complete native diagnostic corpus, differential corpus, typed view checks, and both complexity gates pass without skips.
The integrated checks also pass the upstream LICM tests on x86-64 and Wasm.

`portable.py` compiles the real checker driver for native Linux, Wasm, and ARM64.
It feeds all four cases through each driver and checks diagnostics, exit status, and a nonzero balanced leak census.
`portable.json` records hashes and outcomes.
ARM64 uses QEMU 10.0.13. These probes do not constitute the Go ARM64 harness.
Local results do not establish full current-head GitHub CI success.

## Reproduction

Build clean compiler `c4c31eae08f5` with the pinned toolchain.
Compile each declared compiler source with that emitter and the same flags:

```sh
FERN_LEAKCHECK=1 "$BUILDER" -target x86-64-linux -o "$OUTPUT" compiler/fern.fern
```

Use separate clean checkouts for the three source commits and the fixed input.
Run the collector with the resulting binaries:

```sh
python3 measure.py "$SUBJECT" "$BUILDER" "$BASELINE" "$BEFORE" "$AFTER" fixed-input.json \
  --compiler-source-commits \
  43616e4d4cdf1221220e24efb3687d2b28e51bf9 \
  789a7a81934caba617ccbfda196d04a87fc3b8ba \
  8db514acbe999cff1c11b55d9b167664b22297f7
```

Run the repository allocation gate from the candidate checkout:

```sh
scripts/selfhost-alloc-bench allocation-report.txt
FERN_CI_PERF_GATE_MAX_DRIFT=10 scripts/ci-check-perf .github/alloc-baseline.txt allocation-report.txt
python3 docs/benchmarks/checker-view-cache-2026-10-26/portable.py \
  "$PWD/bin/fern" "$(command -v wasmtime)" "$QEMU_AARCH64" portable.json
```

The collector records heap event counts on finite inputs.
No CPU speed, physical memory bound, compatibility parity, or production claim follows.
