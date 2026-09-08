# Checked string-index inlining on ARM64 SSA

Part of coreutils epic #8278 and the wider compiler work tracked in #8822.
This does not change the default backend or close the x86-64 performance gap.

## Compiler change and correctness

The ARM64 SSA backend previously excluded `__str_idx` from its existing
checked-index inline path because a comment assumed a two-word string ABI.
Its actual strings are single-word data pointers with a length at offset -4.
The string helper and checked byte-array helper perform the same unsigned
bounds comparison and byte-address calculation. Reusing that path eliminates
the helper call and its argument/result moves. Bounds checks remain present;
negative and past-end indices still terminate with status 134.

The regression first failed because a helper call remained. Execution tests
cover the first/last byte, NUL, high bytes, empty strings, negative indices,
minimum/maximum i32 values, and multiple inline sites at 2, 4 and 22 registers.
No global inlining budget is raised, and there is no utility-specific fast path.

The preceding args-cache fix (#8913, fixing #8912) is a prerequisite for valid
SSA coreutils measurements. Both SSA binaries below contain that fix. The
baseline compiler is the frozen cache-fix compiler built on `dbd8937d4`; the
candidate is `8064c5a3f` plus this checked-index change. The intervening uniq
merge does not change the compiler or sort. Before/after compilers and binaries
are separately preserved; their hashes are in the raw results.

## Native coreutils measurements

Native ARM64 Linux on Apple M3 Pro, 12 visible CPUs, kernel
6.12.76-linuxkit, GNU coreutils 9.4 and Rust uutils 0.11.0. Binaries use
`-O -target arm64-linux`, with `-backend ssa` for the two SSA variants.
Inputs and executables are on a Linux-local Docker volume, not the shared
macOS checkout. No heavy test suite runs during timing. Environment is
`LC_ALL=C LANG=C TZ=UTC`; timed stdout goes to `/dev/null`.

Before timing each workload, all implementations must agree on stdout digest,
stderr and exit status. Ten rotating-order rounds discard three warmups.
Values below are mean +/- sample standard deviation in milliseconds from
seven samples, including process startup. A 2,000-line pilot passed in
0.490 s; changing only line count to 200,000 took 29.449 s. An independent
repeat of that larger run took 29.743 s.

| 200,000-line workload | Fern default | SSA before | SSA after | GNU 9.4 | uutils 0.11.0 |
|---|---:|---:|---:|---:|---:|
| Random words | 127.580 +/- 5.177 | 89.859 +/- 8.342 | 84.320 +/- 8.684 | 20.548 +/- 0.472 | 8.809 +/- 0.623 |
| Reverse words | 132.274 +/- 11.066 | 95.482 +/- 16.738 | 85.986 +/- 1.449 | 21.173 +/- 1.309 | 10.713 +/- 2.799 |
| Signed integers, `-n` | 454.373 +/- 14.152 | 259.801 +/- 9.135 | 222.198 +/- 11.029 | 39.723 +/- 1.651 | 20.313 +/- 1.971 |
| Check sorted words, `-c` | 16.429 +/- 0.118 | 12.860 +/- 0.241 | 9.413 +/- 0.274 | 5.285 +/- 0.735 | 3.304 +/- 0.294 |
| Shared 128-byte prefix | 310.872 +/- 24.227 | 263.134 +/- 12.981 | 223.139 +/- 13.314 | 40.254 +/- 1.272 | 29.350 +/- 3.323 |

The repeat measured SSA before/after numeric sorting at 257.217 +/- 6.699
versus 214.160 +/- 3.312 ms; checking at 13.335 +/- 0.586 versus
9.533 +/- 0.332 ms; and shared-prefix sorting at 261.986 +/- 16.483 versus
214.877 +/- 8.643 ms. These gains reproduce. Plain word sorting changed from
91.260 +/- 12.111 to 89.847 +/- 10.653 ms in the repeat, so its improvement
is inconclusive. Fern remains slower than GNU and uutils on every larger
workload. Nothing here establishes native x86-64 speed or universal superiority.

Reproduction: use `lang-coreutils-bench:24.04`, mount the checkout at `/work`
and `lang-coreutils-bench-data` at `/bench`. Build the candidate without
overwriting the frozen before binaries:

```sh
go build -o /bench/fern-ssa-index ./cmd/fern
/bench/fern-ssa-index -O -target arm64-linux -backend ssa -o /bench/sort-ssa-index coreutils/sort.fern
uv run --no-project --python /usr/bin/python3 docs/benchmarks/coreutils-string-index-2026-09-08.py 2000 string-index
uv run --no-project --python /usr/bin/python3 docs/benchmarks/coreutils-string-index-2026-09-08.py 200000 string-index
uv run --no-project --python /usr/bin/python3 docs/benchmarks/coreutils-string-index-2026-09-08.py 200000 string-index-repeat
```

Required frozen baseline paths: `/bench/fern-ssa-fix`, `/bench/sort-ssa-fixed`
and default `/bench/sort`. GNU is `/usr/bin/sort`; uutils is the official
ARM64 multicall release at `/bench/uutils/coreutils`, not the older apt package.
See the [cache-fix report](COREUTILS-SSA-ARGS-2026-09-08.md) for that baseline.

Raw data: [pilot](benchmarks/coreutils-string-index-2026-09-08-pilot.json),
[larger run](benchmarks/coreutils-string-index-2026-09-08.json),
[repeat](benchmarks/coreutils-string-index-2026-09-08-repeat.json).

## Wider Fern effects and costs

The [general-workload script](benchmarks/fern-string-index-2026-09-08.py)
compiles existing performance-corpus programs with both frozen compilers.
It verifies exit checksums/stdout/stderr against each other and the default
backend, then measures runtime and compile/link time with seven retained
samples after three warmups, alternating before/after order. The full small
pipeline took 23.954 s; no inputs were scaled. Sort is included for compiler
costs only because its runtime is covered above.

| Program | Runtime before, ms | Runtime after, ms | Static instructions before/after |
|---|---:|---:|---:|
| String symbol scan | 3.405 +/- 0.110 | 3.342 +/- 0.076 | 1,065 / 1,078 |
| String sort | 8.898 +/- 0.095 | 8.942 +/- 0.083 | 2,465 / 2,482 |
| Integer loop | 1.793 +/- 0.066 | 1.780 +/- 0.091 | 23 / 23 |
| Call overhead | 1.430 +/- 0.049 | 1.419 +/- 0.031 | 77 / 77 |
| Array indexing | 1.346 +/- 0.048 | 1.356 +/- 0.029 | 351 / 351 |
| Persistent map insert | 18.404 +/- 0.697 | 18.609 +/- 0.899 | 5,795 / 5,795 |
| Mostly-ASCII UTF-8 validation | 4.656 +/- 0.142 | 4.437 +/- 0.055 | 751 / 770 |
| Lexer-shaped tokenization | 7.524 +/- 0.161 | 6.403 +/- 0.129 | 536 / 544 |

The integer-loop, call-overhead, array-index and persistent-map executables
are byte-identical before/after: their timing differences are not optimization
effects. Tokenization and UTF-8 validation exercise the change outside
coreutils. Static instruction counts use the existing performance script's
indented-assembly-line convention, not dynamically executed instruction counts.
Inlining duplicates the bounds/trap sequence, so some static counts increase.
Sort grows from 45,666 to 46,091 static instructions, while its final linked
file remains 207,082 bytes due to layout padding. Every measured executable's
file size is unchanged; unchanged file size does not imply unchanged code size.
`readelf -l` confirms the executable segment grows from 178,912 to 180,612
bytes, while the next segment still starts at offset 196,608 in both files.

Sort compile/link time was 453.561 +/- 33.580 ms before and
462.942 +/- 46.230 ms after. There is no compiler-throughput gain claimed;
the timings are noisy and small compilation regressions cannot be excluded.
All per-program compile samples, sizes, checksums and binary hashes are in
the [general results](benchmarks/fern-string-index-2026-09-08.json).

A [repeat of the same general pipeline](benchmarks/fern-string-index-2026-09-08-repeat.json)
took 23.690 s. Tokenization improved from 8.721 +/- 0.524 to
7.789 +/- 0.449 ms and UTF-8 validation from 5.041 +/- 0.142 to
4.821 +/- 0.106 ms. Sort compilation measured 455.231 +/- 28.248 versus
448.713 +/- 20.524 ms, reversing the small difference in the first run.
These measurements support runtime gains in the two string-index workloads,
but not a claim of faster compilation.

## Validation status

Targeted bounds/byte/index tests, the complete ARM64 SSA backend package,
SSA CLI roundtrips and debug/release SSA sort GNU parity passed. The combined
post-cache-fix regression and sort run passed in 0.056 and 2.188 s.
`make lint-all` passed after adding benchmark artifacts, exit 0. The full native
ARM64 Linux `go test ./... -timeout=60m` suite passed, exit 0: coreutils
244.321 s, e2e 1649.904 s and e2eselfhost 132.977 s. No timing benchmarks ran
alongside that suite. Existing x86-64 self-host/driver-size CI failures remain
tracked separately in #8894; this change does not repin those baselines.
