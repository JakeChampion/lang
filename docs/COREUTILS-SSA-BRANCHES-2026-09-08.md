# Threading SSA boolean branches

Compiler work for coreutils epic #8278 and issue #8822. This is a generic SSA
optimization, not a sort-specific rewrite or a default-backend cutover.

## Motivation and scope

Profiling the corrected ARM64 SSA sort binary after #8913 and #8914 identified
`magcompare` as the largest numeric-sort instruction consumer. Callgrind on
200,000 signed integers attributed 1,507,625,186 of 3,881,685,290 instructions
to that function. The 2,000-line pipeline passed first. These are instruction
counts, not native timing results. Assembly and the real SSA graph showed
comparisons materialized as booleans, copied through a phi, then branched on.
The backend can fuse a comparison with its branch only when they are local
to the same block.

`ThreadPhiBranches` bypasses a join containing exactly one phi whose only use
is the join's conditional branch. An unconditional predecessor branches on its
own incoming boolean. Successor phi arguments are duplicated into the new
predecessor slots. No computation, call, load or side effect is cloned or
speculated. Conditional predecessors remain on the old join, which ordinary
unreachable-block and phi cleanup remove when possible.

The pass accepts only comparisons and integer/boolean constants 0 or 1 as
incoming values. It does not assume arbitrary integer/address widths are
interchangeable before width resolution. It declines escaping phi results,
extra operations, entry/self/same-target joins, duplicate predecessors,
malformed phi slots and pre-existing successor edges. Updating copied-value
use counts keeps later candidates conservative.

The baseline is the frozen `/bench/fern-ssa-index` and `/bench/sort-ssa-index`
from #8914. The candidate is main `c2f7057ea` plus this pass; intervening merges
do not change sort. Both include the args-cache lifetime fix. Raw results
record compiler and executable hashes. The default-backend binary is an
unchanged control, not an optimization target of this patch.

## Native sort measurements

Native ARM64 Linux on Apple M3 Pro, 12 visible CPUs, Linux 6.12.76-linuxkit.
GNU coreutils 9.4 and official Rust uutils 0.11.0. All data and executables
reside on a Linux-local Docker volume. Builds use `-O -target arm64-linux`,
plus `-backend ssa` for the SSA variants. No heavy tests run during timing.

The script checks stdout digest, stderr and exit status across all five
implementations before timing each workload. Environment is `LC_ALL=C LANG=C
TZ=UTC`; timed stdout goes to `/dev/null`. Ten rotating-order rounds discard
three warmups, retaining seven process-inclusive samples. The 2,000-line pilot
took 0.443 s; changing only count to 200,000 took 27.685 s, with a 27.706 s
independent repeat. Mean +/- sample standard deviation below, in milliseconds.

| 200,000-line workload | Fern default | SSA before | SSA after | GNU 9.4 | uutils 0.11.0 |
|---|---:|---:|---:|---:|---:|
| Random words | 127.714 +/- 3.718 | 86.555 +/- 11.400 | 81.908 +/- 4.764 | 20.362 +/- 0.500 | 8.277 +/- 1.040 |
| Reverse words | 133.526 +/- 10.821 | 82.396 +/- 0.923 | 82.428 +/- 2.359 | 20.548 +/- 1.046 | 8.115 +/- 0.409 |
| Signed integers, `-n` | 457.130 +/- 13.129 | 219.312 +/- 8.301 | 202.465 +/- 5.917 | 38.263 +/- 0.644 | 16.462 +/- 0.649 |
| Check sorted words, `-c` | 16.813 +/- 0.362 | 9.704 +/- 0.590 | 9.401 +/- 0.082 | 4.919 +/- 0.413 | 2.897 +/- 0.066 |
| Shared 128-byte prefix | 314.047 +/- 23.762 | 220.103 +/- 15.267 | 208.059 +/- 6.714 | 37.133 +/- 1.577 | 22.720 +/- 1.757 |

Numeric sorting reproduced at 216.693 +/- 4.045 ms before versus
204.245 +/- 10.402 ms after. The shared-prefix result reversed to
205.583 +/- 6.826 versus 214.705 +/- 25.357 ms; sortedness checking also
reversed. Neither supports a gain claim. Plain/reverse-word differences are
small relative to their variation. The uutils numeric repeat contains a large
outlier, retained in the raw data rather than discarded after inspection.

The evidence supports a repeatable numeric-sort improvement, not universal
speedups. Fern remains slower than GNU and uutils on every large workload.
No emulated result is used to claim native x86-64 performance.

## Wider Fern workloads and compiler costs

The general script builds eight existing benchmark programs with both SSA
compilers. It checks stdout, stderr and exit checksums against each other and
the default backend, then measures runtime and compile/link time. It retains
seven samples after three warmups, alternating before/after order. Two complete
runs took 23.221 s and 23.290 s.

| Program | Runtime before, ms | Runtime after, ms | Static instructions before/after |
|---|---:|---:|---:|
| String symbol scan | 3.899 +/- 0.214 | 4.135 +/- 0.392 | 1,078 / 1,078 |
| String sort | 8.230 +/- 0.129 | 8.247 +/- 0.125 | 2,482 / 2,494 |
| Integer loop | 1.799 +/- 0.116 | 1.818 +/- 0.103 | 23 / 23 |
| Call overhead | 1.337 +/- 0.046 | 1.310 +/- 0.034 | 77 / 77 |
| Array indexing | 1.381 +/- 0.008 | 1.395 +/- 0.025 | 351 / 351 |
| Persistent map insert | 18.236 +/- 0.080 | 18.266 +/- 0.081 | 5,795 / 5,796 |
| Mostly-ASCII UTF-8 validation | 4.719 +/- 0.095 | 4.741 +/- 0.143 | 770 / 764 |
| Lexer-shaped tokenization | 7.891 +/- 1.573 | 7.138 +/- 1.364 | 544 / 541 |

String scan, integer loop, call overhead and array-index executables are
byte-identical; their timing differences cannot be optimization effects.
Tokenization's apparent improvement reversed in the repeat, from
7.467 +/- 0.228 to 8.528 +/- 4.041 ms. No broader runtime gain is established.
Other small timing differences likewise do not establish a useful gain or
regression. Some instruction counts grow even though joins are removed,
because register allocation and block layout also change.

Sort drops from 46,091 to 45,931 static instructions, using the repository's
indented-assembly-line convention. `readelf -l` reports executable segment
size falling from 180,612 to 179,972 bytes. The total linked file stays
207,082 bytes because the next segment remains at offset 196,608. All eight
general executable file sizes are unchanged; this does not imply unchanged
code size.

Sort compile/link time measured 451.241 +/- 24.185 versus
435.269 +/- 12.638 ms, and 449.036 +/- 14.503 versus 435.645 +/- 7.090 ms in
the repeat. This is a narrower result than faster compilation generally:
other compilation measurements are mixed, and persistent-map compilation
increases slightly in both runs with overlapping variation. No self-hosted
compiler throughput or default-backend improvement is claimed.

## Reproduction and raw results

Use `lang-coreutils-bench:24.04` with the checkout mounted at `/work` and
`lang-coreutils-bench-data` mounted at `/bench`. Keep baseline compiler
`/bench/fern-ssa-index`, SSA sort `/bench/sort-ssa-index`, default sort
`/bench/sort`, and official multicall `/bench/uutils/coreutils` unchanged.

```sh
go build -o /bench/fern-ssa-branches ./cmd/fern
/bench/fern-ssa-branches -O -target arm64-linux -backend ssa -o /bench/sort-ssa-branches coreutils/sort.fern
uv run --no-project --python /usr/bin/python3 docs/benchmarks/coreutils-ssa-branches-2026-09-08.py 2000 ssa-branches
uv run --no-project --python /usr/bin/python3 docs/benchmarks/coreutils-ssa-branches-2026-09-08.py 200000 ssa-branches
uv run --no-project --python /usr/bin/python3 docs/benchmarks/coreutils-ssa-branches-2026-09-08.py 200000 ssa-branches-repeat
uv run --no-project --python /usr/bin/python3 docs/benchmarks/fern-ssa-branches-2026-09-08.py
```

General results are written to `/bench/ssa-branches-general/results.json`;
preserve that file before repeating. The two scripts retain baseline hashes,
individual samples, input/source hashes and output parity verdicts.

Raw sort results: [pilot](benchmarks/coreutils-ssa-branches-2026-09-08-pilot.json),
[larger run](benchmarks/coreutils-ssa-branches-2026-09-08.json),
[repeat](benchmarks/coreutils-ssa-branches-2026-09-08-repeat.json).
General results: [first](benchmarks/fern-ssa-branches-2026-09-08.json),
[repeat](benchmarks/fern-ssa-branches-2026-09-08-repeat.json).

## Validation status

The structural regression failed before the pass and passes after it.
Targeted tests cover all comparison kinds, truth tables, successor phis,
partially bypassed joins, loops, skipped faults and conservative exclusions.
The full native ARM64 Linux repository suite passed with
`go test ./... -timeout=60m`: coreutils 226.203 s, e2e 1640.903 s,
self-hosted e2e 119.526 s and SSA 178.815 s. The process exited successfully.

ARM64 debug/release short-circuit execution, the ARM64 CLI roundtrip corpus,
native GNU sort parity on default and SSA debug/release builds, and x86-64
debug/release short-circuit execution under QEMU passed. The new WebAssembly
test passed under Wasmtime. Its first draft used unsupported cross-function
SSA calls; the final test keeps the same branches within one function.
Existing Wasm CLI validation requiring `wasm-tools` was skipped because that
tool is absent from the test image, not counted as a pass. `make lint-all`
passed on the final source tree.
