# Byte-oriented filename matching

`fnmatch_bytes([u8], [u8])` accepts raw patterns and names, including NUL and
invalid UTF-8. `fnmatch_bytes_text([u8], string)` matches a raw pattern against
an existing text value, and the text-only API remains available. All three
specialize one matcher through a private input trait. Borrowed wrappers stay
within the byte entry points. Class names are compared against ASCII literals
without constructing strings from arbitrary pattern bytes.

The matcher preserves flags-zero, C-locale behavior: wildcards cross slashes,
leading dots are ordinary bytes, and backslashes quote the following byte.
Unknown classes invalidate a pattern even when negated. An unclosed bracket
falls back to matching a literal opening bracket. Scalar match statuses avoid
allocating tuples for ordinary matches and star retries; range parsing retains
its existing tuple result.

Darwin follows GNU's gnulib fallback for collating symbols and equivalence
classes: `[.x.]` and `[=x=]` are ordinary bracket contents. Linux retains
glibc's special interpretation. The corpus checks both interpretations,
including the literal suffix left after Darwin's first closing bracket.

## Validation

The October 4 repair snapshot includes main `cac927ea8`. Its shared fixture checks
all 256 byte values, nonzero-offset views, retained aliases, raw collating
symbols, malformed UTF-8 class names, and agreement among all three APIs.
Linux Go interpreter/native/WASI and primary native/WASM matrices pass,
along with the full unit suite and all lint gates. Go and primary target
groups pass alongside suspension-frame, scheduler and WASM fetch regressions.
GNU and primary comparisons for ls, dir, vdir, dircolors, cp, mv and install
pass in 5.675 and 24.511 seconds.

Darwin Go/interpreter/WASI and primary native/interpreter matcher groups
pass in 3.020 and 15.553 seconds. The GNU listing/dircolors/install group
passes in 13.870 seconds; all seven primary consumer comparisons pass in
36.955 seconds. Actual reproduced stage-2 runs pass on Darwin, core WASM and
the interpreter. Native allocations/frees are 4,805/4,805 and core WASM
4,736/4,736, both with zero live bytes.

Fresh Linux and Darwin bootstraps use the repository pin
`stage0-20261001-c891ebc`. Linux stages 2 and 3 match at 12,913,600 bytes,
SHA-256 `67898227c33521bf74c3657b1c45a406008bbf325d5a88ac319f0c5a552f164b`.
Darwin stages 2 and 3 match at 13,144,785 bytes,
SHA-256 `85a30d6664eb5f7ddb1848e9658f212b5b91e2cf0c01bfe8c9d2bc92e9d598b5`.
Stage 1 differs on both hosts because the pin predates generator changes.
The final audit-driver and regression-fixture edits leave the compiler's
source closure unchanged; subsequent Darwin checks reuse that fixed point.
Three pinned x86 driver sizes pass: path probe 4,312,128 bytes, checker
2,538,272 bytes and compiler 11,468,216 bytes. CI checks all eleven drivers.

Integration exposed three failures from current main. The provided-body
inventory now lists all 17 emitted task helpers; its audit checks actual
runtime declarations and rejects unknown names. Task-only parking calls
`__task_park` directly, keeping the blocking native reactor fallback out of
WASM fetch builds. Generic promotion now scans free identifiers, so a local
`first` in `std/string` cannot erase the unrelated generic function `first`.
The scope regression covers parameters, local captures, patterns and reads
before a later shadow. It fails under the pre-fix compiler and passes on
Darwin, core WASM and the Go interpreter, with zero unmatched x86 allocations.
The verifier models all 12 regression bodies and resolves 28 calls; the
original failing generic fixture resolves 1,913 calls in 948 bodies.

The subsequent external branch merge `8984d08c1` adds main `0129e5d95`'s
retired-backend test cleanup and documentation. It leaves the primary
compiler and standard library unchanged. Both relocated map-rebinding
regressions and the full Linux unit suite pass on the combined tree.

The expanded Darwin GNU cp/mv comparison fails 11 subcases. An unchanged
`dcc065645` snapshot reproduces exactly the same case set: sparse copying,
reflinks, backup suffixes and trailing-slash/root handling. These are existing
entries in the Darwin failure catalogue, not passing results. This change
adds no exceptions. The prepared raw-copy migration separately fixes the
swapped Darwin SEEK_DATA/SEEK_HOLE values.

## Darwin listing corrections

The first integration run found seven GNU ls failures. Darwin's device
number uses an 8-bit major above a 24-bit minor. GNU's BSD birth-time rule
treats zero seconds or an invalid nanosecond fraction as unknown. Applying
both rules fixes the complete ls, dir and vdir corpora. Those tests and
dircolors now leave the Darwin failure catalogue, so CI holds them to parity.

The same reproduced compiler above builds both size variants; only listing
differs. An intermediate implementation grew Linux ls by 136 bytes because
platform branches prevented scalar device helpers from inlining. Selecting
the platform at the rendering sites removes that overhead.

| LS artifact | Before | After |
| --- | ---: | ---: |
| x86-64 Linux file | 461,128 bytes | 461,128 bytes |
| ARM64 Darwin file | 515,873 bytes | 515,873 bytes |
| Darwin code | 401,880 bytes | 401,840 bytes |
| Darwin unwind | 33,860 bytes | 33,860 bytes |
| Darwin data | 49,160 bytes | 49,160 bytes |

## Controlled matcher measurements

The before module is from `a69b8ca2f`; only the matcher changes. Both variants
use the reproduced compiler above and its matching standard library. The
ARM64 Darwin benchmark checks every result, uses two warmups and seven
alternating samples, and scales from 16,384 to 1,048,576 calls. Task-owned
heavy jobs were idle; the desktop was not isolated. Allocation counts are
collected separately from timed binaries.

The pattern/input pairs are `terminal` / `terminal`, `*term*color` /
`xterm-truecolor`, `[[:alpha:]][[:digit:]][[:space:]]` / `a5` followed by a
space, and `[![:unknown:]]` / `a`. The first three must match; the last
must fail on every iteration.

| Workload | Before median | After median | Before range | After range |
| --- | ---: | ---: | ---: | ---: |
| Literal | 23.016 ms | 19.700 ms | 22.610-23.686 ms | 19.459-20.503 ms |
| Star | 83.011 ms | 83.967 ms | 82.441-84.453 ms | 83.148-88.428 ms |
| Classes | 111.864 ms | 82.398 ms | 111.193-116.298 ms | 81.358-85.168 ms |
| Invalid class | 41.353 ms | 28.009 ms | 41.128-43.223 ms | 27.639-28.893 ms |

Literal, class and invalid-class ranges are disjoint. Star ranges overlap,
so this run does not establish a star-matching speedup.

| Workload | Before allocations | After allocations |
| --- | ---: | ---: |
| Literal | 1,048,583 | 7 |
| Star | 9,437,191 | 7 |
| Classes | 10,485,767 | 7 |
| Invalid class | 4,194,311 | 7 |

Both variants balance allocations and frees. The seven allocations cover
the whole measured program, not each call. These workloads do not establish
allocation-free behavior for every pattern shape.

Both native benchmark files occupy 49,745 bytes. Code shrinks from 18,016
to 16,836 bytes; unwind data grows from 3,156 to 3,660 bytes; data remains
792 bytes. No size baseline changes. This prepares raw dircolors parsing;
it does not complete that consumer or the remaining #5714 acceptance work.
