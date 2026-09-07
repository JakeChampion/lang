# A coreutils workload exposes an ARM64 SSA argument-cache lifetime bug

Part of #8278's broader objective: use coreutils to improve Fern itself.
No default-backend switch is proposed.

## Correctness first

On main `dbd8937d4`, ARM64 SSA sort compiled but ignored options, returned empty
output for a file operand, or crashed with SIGSEGV when given `-n` and a file.
The same numeric input on stdin was sorted lexically despite `-n`.

The cause was the `args()` runtime helper, not the sorting algorithm. It
memoised one array but initialised its reference count to 1. A local caller
such as `gnu.prog()` could drop the cached array and its strings. Further calls
returned freed memory; allocator reuse corrupted the option scanner and the
freelist. The default backends already mark their argument caches immortal.

The fix uses that same static reference-count sentinel on ARM64 SSA's cached
container. The cache and its element references live until process exit.
It does not disable reclamation for other arrays or add per-call allocations.

The reduced runtime regression drops an `args()` result, allocates a buffer
of the same size class, then calls `args()` again. Before the fix its length
changes from 1 to 8; after the fix it remains 1. This was tested with 2, 4 and
22 allocatable registers. The complete GNU sort corpus now also runs through
SSA in debug and release mode, rather than relying only on small compiler
examples to establish correctness.

## Newly usable backend, not a speedup over a broken binary

The following compares the unchanged default backend with the corrected SSA
backend. The old SSA sort binary is not a valid performance baseline because
it fails output parity. Both use `-O -target arm64-linux`; SSA additionally
uses `-backend ssa`. Source is `dbd8937d4`, with only the argument-cache fix for
the SSA candidate. Both binaries contain the already-merged sort mismatch
improvement from #8910.

Native ARM64 Linux on Apple M3 Pro, 12 visible CPUs, kernel
6.12.76-linuxkit, glibc 2.39. GNU coreutils 9.4 and Rust uutils 0.11.0.
Linux-local inputs/binaries, `LC_ALL=C LANG=C TZ=UTC`, stdout to `/dev/null`.
No heavy test suite ran during measurement. Each implementation must agree on
stdout digest, stderr and exit status before timing. Ten rotating-order
rounds discard three warmups; figures are mean +/- sample standard deviation
in milliseconds over the remaining seven samples, including process startup.

| Workload, 200,000 lines | Fern default | Fern SSA fixed | GNU 9.4 | uutils 0.11.0 |
|---|---:|---:|---:|---:|
| Random words | 132.410 +/- 6.764 | 94.966 +/- 13.829 | 20.593 +/- 0.969 | 10.363 +/- 2.036 |
| Random words, reverse | 130.919 +/- 5.019 | 93.135 +/- 9.214 | 20.785 +/- 1.054 | 8.674 +/- 1.199 |
| Shuffled signed integers, `-n` | 462.425 +/- 16.340 | 264.733 +/- 17.150 | 38.831 +/- 0.776 | 19.897 +/- 4.155 |
| Check sorted words, `-c` | 17.150 +/- 0.399 | 13.765 +/- 0.680 | 5.229 +/- 0.240 | 3.360 +/- 0.207 |
| Words with a shared 128-byte prefix | 315.120 +/- 29.713 | 290.365 +/- 32.167 | 41.205 +/- 2.588 | 38.386 +/- 11.560 |

Numeric and ordinary word sorting improve versus Fern's default backend on
this run. The smaller prefix difference is noisy and needs more measurement.
Fern remains slower than both comparison implementations on every workload
here. Native x86-64 performance and universal backend readiness are not claimed.

A 2,000-line pilot completed in 0.504 s; changing only line count to 200,000
completed in 23.266 s. In the startup-heavy pilot, ordinary word sorting was
slower through SSA. Preserve both [pilot samples](benchmarks/coreutils-sort-ssa-2026-09-08-pilot.json)
and [larger samples](benchmarks/coreutils-sort-ssa-2026-09-08.json), including
binary/input hashes. The [script](benchmarks/coreutils-sort-ssa-2026-09-08.py)
reuses the earlier sort experiment and requires a disposable Linux `/bench`:

```sh
uv run --script docs/benchmarks/coreutils-sort-ssa-2026-09-08.py 2000 ssa-pilot
uv run --script docs/benchmarks/coreutils-sort-ssa-2026-09-08.py 200000 ssa-pilot
```

Place the default executable at `/bench/sort`, corrected SSA executable at
`/bench/sort-ssa-fixed`, GNU 9.4+ at `/usr/bin/sort`, and official uutils 0.11.0's
ARM64 multicall executable at `/bench/uutils/coreutils`.

The linked binaries measured 339,857 bytes for default and 207,082 for SSA.
Before and after the cache correction, SSA sort is exactly 207,082 bytes;
only two bytes differ, within the instruction that initialises the cache's
reference count. No compiler-throughput improvement is claimed for this fix.

## Validation

The reduced runtime test failed before the fix and passed after it (0.042 s).
Debug/release SSA sort GNU parity passed (3.878 s), as did the existing ARM64
SSA CLI roundtrip cases (7.713 s). `make lint-all` and the full native ARM64
Linux `go test ./... -timeout=60m` suite passed, exit 0: coreutils 238.960 s,
e2e 1639.527 s, e2eselfhost 130.113 s. Existing main x86-64 self-host CI failures and
driver-size baseline drift are tracked separately by #8894; this change does
not update those size baselines.
