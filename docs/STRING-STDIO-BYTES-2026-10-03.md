# Raw bytes in GNU-style buffered output

`gnu.Stdio` retains pending output as an immutable byte array. Its existing
`fwrite(string)` entry point remains available, alongside `fwrite_bytes(u8[])`
and `fwrite_bytes_range(u8[], lo, hi)`. The range is clamped to the input;
an empty range has no effect. Buffer boundaries can now split UTF-8 characters
without constructing invalid strings.

The write ordering, block size selection and sticky-error behavior follow
the existing GNU compatibility contract. A retained stream value keeps its
pending bytes when another copy appends or flushes. Short appends use array
reuse; larger appends use a private builder. Direct writes lend byte views
to Writer, including subranges of text or raw arrays. Only bytes retained
in the pending buffer need an owned copy; a direct write never constructs
a partial string or an owned array for its range.

## Validation

The shared fixture has 183 cases covering all byte values, NUL, malformed
UTF-8, clamped and empty ranges, retained stream aliases, closed descriptors,
and Unicode across buffer boundaries. Sizes cover the append threshold and
4096-, 8192- and 65536-byte boundaries. Four repeated large Unicode writes
also retain the input string while crossing buffered and direct-write paths.

The validated PR snapshot is `01fe6a2529`, including main `9c8eb0032`.
The full Linux unit suite,
all lint gates, Stdio target groups, and GNU/primary consumer comparisons
pass, including the df operand-device correction. Darwin Go and source-built
primary tests pass in 9.536 and 38.977 seconds. The reproduced compiler
passes all 366 native Darwin/core-WASM corpus runs in 3.059 seconds, with
balanced allocations and zero live bytes.

The Darwin bootstrap explicitly uses the previously reproduced local compiler
with SHA-256 `27c93618d8c0b4d9eb15a8e3eef3664c4a0e70da18eded1dd842260754649359`
as `STAGE0`. All three resulting stages are identical at 13,128,209 bytes,
SHA-256 `89e577b9ef1315b819cbb198c3c49e8603558fcacd9d60cb40268b36b5c61d52`.
This is the measured local-seed chain; the repository's pinned-seed bootstrap
remains a separate CI gate.
Main's Go SSA retirement and compiler/checker size baselines are retained;
all three affected driver checks pass the unchanged 5% gate. The path probe,
compiler and checker measure 4,283,816, 11,426,392 and 2,532,088 bytes.
Assembler record parity/refusals, checker codes, scheduler/fetch, IR registry,
LSP and cache-advice integration checks also pass. The benchmark
sections below identify their own compiler and source revisions.

## Measurements before the direct-write follow-up

Both versions use the reproduced primary compiler with SHA-256
`3f4f3a40bf167dee51cbdc2d90e396cb3999a3bd34ccc741d479d42904d9682a`
and its matching standard library. The before module is `coreutils/lib/gnu.fern`
at `38f4a269d`; the after module is this byte-buffer implementation. No other
source differs. Task-owned heavy jobs were idle; the desktop was not isolated.

The program creates one Stdio, repeatedly writes the same string, then closes
stdout. Workloads use seven ASCII bytes, the seven UTF-8 bytes of `€🙂`, 4096
ASCII bytes, or 65536 ASCII bytes per call. The repetition count is the total
requested byte count divided by the piece length. A 131072-byte pilot passes
before scaling only the requested count to 8388608. Exact stdout and balanced
memory counts are checked separately before timing to `/dev/null`. Each pair
uses two warmups and seven samples in alternating order.

| Piece | Before median | After median | Before allocations | After allocations |
| --- | ---: | ---: | ---: | ---: |
| 7 ASCII bytes | 38.722 ms | 26.656 ms | 1,469,763 | 11,588 |
| 7 Unicode bytes | 37.599 ms | 26.366 ms | 1,469,763 | 11,588 |
| 4096 ASCII bytes | 2.907 ms | 3.203 ms | 7,205 | 6,180 |
| 65536 ASCII bytes | 2.565 ms | 2.948 ms | 1,059 | 1,183 |

The small-write ranges are disjoint: ASCII takes 37.861-39.526 ms before and
26.300-29.661 ms after; Unicode takes 37.407-39.752 and 26.218-27.819 ms.
Both larger cases are slower with disjoint ranges: 4096-byte writes take
2.864-2.975 ms before and 3.130-3.285 ms after; 65536-byte writes take
2.523-2.612 and 2.853-3.078 ms. Large writes still pay for partial-range
copies, byte-buffer construction and Writer view dispatch. The small-write
allocation reduction is exact; the timing result applies to those workloads.

Both native benchmark files occupy 83,073 bytes. Code grows from 41,680 to
42,464 bytes and unwind data from 7,300 to 7,516 bytes; data grows from 4,144
to 4,168 bytes. The extra paths support raw input, range handling and byte buffering.
Stdio itself needs no size baseline change. A later main integration crossed
the compiler path-probe gate. Its separate [size investigation](SELFHOST-PATH-PROBE-SIZE-2026-10-04.md)
attributes the accumulated compiler growth and removes unused verdict report
construction before updating that driver's measured baseline.

## Borrowed direct ranges, October 4

The review follow-up removes the temporary owned array from direct subrange
writes. Text first lends `as_bytes()`, then selects a borrowed byte subview;
raw input lends its array subview directly. This avoids constructing a
partial UTF-8 string even when a block boundary splits a character.

Both benchmark versions use the reproduced compiler with SHA-256
`48efd5540bac3b85b564beb4b49e0c0d3b2b6aea510c1652c0328258909f66fe`
and its matching standard library. Compiler sources are unchanged by this
follow-up. The before GNU library is from `74c69bdf8`; only that module
differs in the after build. Task-owned heavy jobs were idle, but the desktop
was not isolated. A 131072-byte pilot precedes 8388608 bytes with the same
exact-output and allocation checks, two warmups and seven alternating samples.
The new raw-range workloads retain a byte array with one sentinel at each end
and repeatedly write the interior range.

| Piece | Before median | After median | Before allocations | After allocations |
| --- | ---: | ---: | ---: | ---: |
| 7 ASCII bytes | 25.708 ms | 26.380 ms | 11,588 | 11,588 |
| 7 Unicode bytes | 25.456 ms | 25.506 ms | 11,588 | 11,588 |
| 4096 ASCII bytes | 3.052 ms | 3.079 ms | 6,180 | 6,180 |
| 65536 ASCII bytes | 2.885 ms | 2.806 ms | 1,183 | 930 |
| 4096-byte raw range | 3.461 ms | 3.402 ms | 8,230 | 8,230 |
| 65536-byte raw range | 2.970 ms | 2.659 ms | 1,188 | 932 |

All timing ranges overlap. The allocation reductions on large direct writes
are measured; these timing samples do not establish a speedup. Both native
files occupy 83,089 bytes. Text shrinks from 43,056 to 43,032 bytes, while
unwind data stays at 7,572 bytes and data at 4,168 bytes. No baseline changes.

This enables byte-oriented consumers such as dircolors. It does not complete
their conversion or the remaining producer audit for #5714.
