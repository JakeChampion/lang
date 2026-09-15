# Share import traversal during selfhost source staging

`WriteSelfHostAsmProject` stages several compiler roots. Those roots import
many of the same files. Previously, `CopySelfHostFiles` computed a separate
transitive closure for every root and avoided duplicate copies only afterward.
That still reread and rescanned shared dependencies for every root.

The staging helper now traverses all requested roots with one visited set.
It preserves first-visit order, the union of required sources and the existing
missing-local-import errors. A normal single-root cache-key traversal uses the
same implementation. The visited set lives only for one call: it cannot hide
source changes between builds or turn a missing import into a stale cache hit.

## Measurements

The source-staging benchmark uses the real compiler root set. After other
local validation finished, four fresh-process trials ran in before/after/
after/before order, with three one-iteration samples per process. Host:
Mac15,6, Apple M3 Pro, 12 CPUs, 36 GiB RAM, Go 1.26.0 Darwin ARM64.

| Revision | Samples | Mean milliseconds/op | Mean bytes/op | Mean allocations/op |
| --- | ---: | ---: | ---: | ---: |
| Before | 6 | 424.844 | 80249545 | 3272.2 |
| Shared traversal | 6 | 110.154 | 25667641 | 1118.3 |

That is 3.86x faster staging and 68% fewer allocated bytes for this workload.
These measurements cover staging, not compilation or whole CI. Compiler source
contents were identical across revisions. Earlier exploratory samples with
concurrent target validation are excluded from this timing comparison.

Reproduce on each revision with the same benchmark:

```sh
go test ./internal/e2eharness -run '^$' \
  -bench '^BenchmarkStageSelfHostAsmProject$' -benchtime=1x -count=3 -benchmem
```

The benchmark was named `BenchmarkCIStageAsmProject` in the temporary baseline
probe; its body is identical to the committed benchmark.

## Validation

Regression tests cover overlapping and repeated roots, cycles, missing later
roots and dependencies, and byte-for-byte equality between staged real compiler
files and the union of individual source closures. Existing cache-key tests
continue to check transitive source edits and unrelated-file invariance.
