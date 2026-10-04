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

The October 4 snapshot includes main `dcc065645`. Its shared fixture checks
all 256 byte values, nonzero-offset views, retained aliases, raw collating
symbols, malformed UTF-8 class names, and agreement among all three APIs.
Linux Go interpreter/native/WASI and primary native/WASM matrices pass,
along with the full unit suite and all lint gates. Go and primary target
groups take 1.203 and 28.373 seconds. GNU and primary comparisons for ls,
dir, vdir, dircolors, cp, mv and install pass in 5.728 and 24.372 seconds.

Darwin Go/interpreter/WASI and primary native/interpreter matcher groups
pass in 2.623 and 37.497 seconds. The GNU listing/dircolors/install group
passes in 13.881 seconds; all seven primary consumer comparisons pass in
37.187 seconds. Actual reproduced stage-2 runs pass on Darwin, core WASM and
the interpreter. Native allocations/frees are 4,805/4,805 and core WASM
4,736/4,736, both with zero live bytes. All 5,520 Go/Fern files match the
frozen validated source.

The fresh Darwin bootstrap explicitly uses the local compiler with SHA-256
`89e577b9ef1315b819cbb198c3c49e8603558fcacd9d60cb40268b36b5c61d52`
as `STAGE0`. Stages 1, 2 and 3 are identical at 13,128,225 bytes, SHA-256
`11a977b65e7f17b6791feb06d9f8108f8df9f59bcfa2f6b4a91416f67a803d32`.
The repository's pinned bootstrap remains a separate CI gate.

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
| x86-64 Linux file | 461,696 bytes | 461,696 bytes |
| ARM64 Darwin file | 515,873 bytes | 515,873 bytes |
| Darwin code | 402,728 bytes | 402,688 bytes |
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
| Literal | 22.319 ms | 19.496 ms | 21.675-25.962 ms | 18.551-20.420 ms |
| Star | 80.822 ms | 81.080 ms | 80.292-82.309 ms | 80.207-81.609 ms |
| Classes | 107.545 ms | 79.766 ms | 107.133-109.355 ms | 79.279-81.607 ms |
| Invalid class | 39.410 ms | 27.437 ms | 38.952-39.884 ms | 27.227-29.381 ms |

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

Both native benchmark files occupy 49,745 bytes. Code shrinks from 18,080
to 16,900 bytes; unwind data grows from 3,156 to 3,660 bytes; data remains
792 bytes. No size baseline changes. This prepares raw dircolors parsing;
it does not complete that consumer or the remaining #5714 acceptance work.
