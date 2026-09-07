# Sort byte comparison: first measured improvement for #8822

Fern still trails GNU coreutils and Rust uutils on these workloads. Replacing
sort's scalar byte comparison with the existing `__mismatch` primitive improves
plain and common-prefix sorting; it does not fix numeric comparison or the
compiler's large-unit inlining cutoff.

## Environment and method

- Apple M3 Pro, native ARM64 Linux in Docker Desktop, 12 visible CPUs.
- Linux 6.12.76-linuxkit, glibc 2.39, GNU coreutils 9.4, uutils 0.11.0.
- Baseline source `e3029537e`; candidate is that source with only `cmp_bytes`
  changed in `coreutils/sort.fern`. Both built by the same Go-hosted Fern
  compiler with `-O -target arm64-linux`. This is a dirty-tree experiment;
  the raw report identifies both binaries by SHA-256.
- All inputs and Fern/uutils binaries are on a Linux-local Docker volume,
  not the macOS bind mount. GNU is installed in the container.
- `LC_ALL=C LANG=C TZ=UTC`, identical input per workload, stdout to `/dev/null`.
  No shell or pipeline in the timed command. GNU and uutils use their defaults,
  including their normal threading behavior.
- Verify stdout digest, stderr and exit status agree across all four binaries
  before timing each workload. This is supplemental to the GNU oracle corpus.
- Ten rounds with rotating implementation order; discard three warmup rounds,
  retain seven wall-clock samples. Mean plus/minus sample standard deviation
  below, in milliseconds. Timings include process startup and Python process
  supervision. No competing test suite ran during measurement.
- A 2,000-line pilot completed in 0.668 seconds. The same pipeline with only
  line count raised to 200,000 completed in 48.875 seconds.

## Results

| Workload, 200,000 lines | Fern before | Fern after | GNU 9.4 | uutils 0.11.0 |
|---|---:|---:|---:|---:|
| Random 11-byte words | 146.606 +/- 12.485 | 130.457 +/- 5.794 | 22.263 +/- 0.958 | 10.811 +/- 2.441 |
| Same words, `-r` | 145.239 +/- 2.222 | 137.506 +/- 10.724 | 22.004 +/- 0.596 | 10.662 +/- 1.512 |
| Random signed integers, `-n` | 454.226 +/- 8.112 | 463.690 +/- 12.578 | 40.631 +/- 0.926 | 21.846 +/- 2.413 |
| Sorted words, `-c` | 19.556 +/- 0.831 | 16.788 +/- 0.338 | 5.416 +/- 0.631 | 3.600 +/- 0.293 |
| Words with a shared 128-byte prefix | 2299.054 +/- 22.187 | 315.164 +/- 27.557 | 42.036 +/- 2.875 | 33.804 +/- 5.211 |

The common-prefix workload benefits most because repeated equal bytes move
through the existing block comparison instead of the generated per-byte loop.
It is an additional workload, not a replacement for random words or numbers.
The numeric mean is higher after the change; this run does not establish a
numeric speedup, and further repetitions are needed to distinguish a small
regression from timing variation. Reverse-sort samples also have appreciable
variance. These are ARM64 measurements, not evidence about native x86-64.

### Quiet-machine repeat after the full suite

A second run of the identical pilot and 200,000-line pipeline completed in
0.607 s and 47.669 s respectively. Its [raw samples](benchmarks/coreutils-sort-2026-09-07-repeat.json)
retain every result, including an outlier in uutils reverse sorting.

- Plain words: Fern 143.968 +/- 8.560 ms before, 126.902 +/- 2.645 ms after.
- Shared prefix: Fern 2270.897 +/- 17.402 ms before, 304.517 +/- 23.869 ms after.
- Numeric: Fern 453.659 +/- 9.606 ms before, 459.058 +/- 11.551 ms after.
  The seven paired after-minus-before differences average 5.399 ms with a
  7.651 ms standard error. This repeat still does not establish a numeric
  improvement or a reliable small regression; the higher mean is retained
  rather than removed from the report. Numeric performance remains follow-up
  work under #8822.

The plain and common-prefix improvements reproduced. Fern still trails both
reference implementations, and no overall-fastest claim is made.

## Reproduction and evidence

The [raw samples](benchmarks/coreutils-sort-2026-09-07.json) include input
sizes and hashes. The [experiment script](benchmarks/coreutils-sort-2026-09-07.py)
preserves the generator and measurement loop; native-ELF and minimum-GNU-version
checks were added afterward without changing the measured workloads.

In separate baseline and candidate worktrees, build with:

```sh
go build -o /bench/fern ./cmd/fern
/bench/fern -O -target arm64-linux -o /bench/sort coreutils/sort.fern
```

Keep the baseline executable as `/bench/sort-before` and the candidate as
`/bench/sort`. Extract the official uutils 0.11.0
`aarch64-unknown-linux-gnu` release into `/bench/uutils`, retaining its `coreutils`
multicall executable. Use a disposable Linux-local `/bench` directory or volume;
the script overwrites its generated inputs and report names.

```sh
uv run --script docs/benchmarks/coreutils-sort-2026-09-07.py 2000 interleaved
uv run --script docs/benchmarks/coreutils-sort-2026-09-07.py 200000 interleaved
```

The new shared oracle cases cover empty and equal spans, prefixes, all mismatch
positions around 4/8/16/32/64-byte boundaries, NUL and high bytes, reverse,
unique, stable/key and zero-terminated sorting. They are used by both native
and self-hosted sort tests. Targeted GNU and self-host sort parity, all six
native/self-host mismatch sweeps (x86-64 under QEMU, native ARM64 and WASM),
and `make lint-all` passed. The full Linux `go test ./... -timeout=60m` passed,
including coreutils (277.341 s), e2e (1700.390 s) and e2eselfhost (158.665 s).
