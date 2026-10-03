# Raw bytes in GNU-style buffered output

`gnu.Stdio` retains pending output as an immutable byte array. Its existing
`fwrite(string)` entry point remains available, alongside `fwrite_bytes(u8[])`
and `fwrite_bytes_range(u8[], lo, hi)`. The range is clamped to the input;
an empty range has no effect. Buffer boundaries can now split UTF-8 characters
without constructing invalid strings.

The write ordering, block size selection and sticky-error behavior follow
the existing GNU compatibility contract. A retained stream value keeps its
pending bytes when another copy appends or flushes. Short appends use array
reuse; larger appends use a private builder. Whole text writes lend
`as_bytes()` to Writer. Partial ranges still copy, and this change makes no
claim that every write is allocation-free.

## Validation

The shared fixture has 179 cases covering all byte values, NUL, malformed
UTF-8, clamped and empty ranges, retained stream aliases, closed descriptors,
and Unicode across buffer boundaries. Sizes cover the append threshold and
4096-, 8192- and 65536-byte boundaries.

The current Go Darwin fixture passes in 5.285 seconds. The reproduced primary
compiler passes the same fixture on Darwin and core WASM, including balanced
allocation counts and zero live bytes, in 2.289 seconds. Linux Go and primary
target groups pass in 0.671 and 39.505 seconds. GNU consumer checks pass in
5.473 seconds, and primary/native consumer comparisons in 20.397 seconds.
The full Linux unit suite and all lint gates pass on that snapshot. Validation
includes the prepared df operand-device correction.

After integrating Writer's bootstrap cleanup repair `99abe4870`, the Linux
Go and primary groups pass again in 0.677 and 40.780 seconds. GNU consumer
checks pass in 5.736 seconds and primary/native comparisons in 20.965 seconds.
All lint gates pass, and all 5,923 Go/Fern files match the integrated snapshot.
The source-built Darwin primary test passes in 36.586 seconds. The earlier full unit pass
belongs to the preceding Stdio snapshot; the Writer repair separately passes
the full suite, and full integrated CI remains a merge gate.

The next integration includes main `26b82ea7c` and the borrowed-map alias
repair `80d197eb8`. Linux Go and primary fixture groups pass in 0.650 and
39.734 seconds; GNU consumers and primary/native comparisons pass in 5.409
and 20.489 seconds. The full unit suite and all lint gates pass. All 5,928
Go/Fern files match the frozen snapshot. Darwin Go and primary tests pass,
and the actual reproduced compiler passes all 179 fixture cases on native
Darwin and core WASM with balanced allocation counts in 3.263 seconds.
The 208 primary compiler and standard-library source files match that
compiler's reproduced source tree exactly.

## Controlled measurements

Both versions use the reproduced primary compiler with SHA-256
`571e6324d7055bdb7782ec768a8543b7715140a669a054498db01db3d3e2b450`
and its matching standard library. The before module is `coreutils/lib/gnu.fern`
at `26b82ea7c`; the after module is this byte-buffer implementation. No other
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
| 7 ASCII bytes | 38.501 ms | 26.638 ms | 1,469,763 | 11,588 |
| 7 Unicode bytes | 38.119 ms | 26.677 ms | 1,469,763 | 11,588 |
| 4096 ASCII bytes | 3.158 ms | 3.448 ms | 7,205 | 6,180 |
| 65536 ASCII bytes | 2.687 ms | 3.014 ms | 1,059 | 1,183 |

The small-write ranges are disjoint: ASCII takes 37.863-39.767 ms before and
25.935-27.409 ms after; Unicode takes 37.461-39.071 and 26.039-28.272 ms.
The 4096-byte case is slower with disjoint ranges, 3.038-3.217 versus
3.373-3.542 ms. The 65536-byte ranges overlap. This is not a general speedup:
large writes still pay for partial-range copies, byte-buffer construction
and Writer view dispatch. The small-write allocation reduction is exact.

Both native benchmark files occupy 83,073 bytes. Code grows from 41,488 to
42,272 bytes and unwind data from 7,276 to 7,492 bytes; data grows from 4,144
to 4,168 bytes. The extra paths support raw input, range handling and byte buffering.
No size baseline changes are needed.

This enables byte-oriented consumers such as dircolors. It does not complete
their conversion or the remaining producer audit for #5714.
