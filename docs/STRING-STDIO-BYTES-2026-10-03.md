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

## Controlled measurements

Both versions use the reproduced primary compiler with SHA-256
`fc15892a54e9f5d5017cd0b3ad748b31b6eb30b012237d136d6e6ac2b6e7ec1a`
and its matching standard library. The before module is `coreutils/lib/gnu.fern`
at `68623a892`; the after module is this byte-buffer implementation. No other
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
| 7 ASCII bytes | 38.193 ms | 27.638 ms | 1,469,763 | 11,588 |
| 7 Unicode bytes | 37.963 ms | 27.198 ms | 1,469,763 | 11,588 |
| 4096 ASCII bytes | 3.519 ms | 4.203 ms | 7,205 | 6,180 |
| 65536 ASCII bytes | 3.216 ms | 3.436 ms | 1,059 | 1,183 |

Only the Unicode sample ranges are disjoint: 37.089-40.852 ms before and
25.694-28.338 ms after. The other ranges overlap. The larger-write medians
are worse, and this measurement does not establish a general speedup. The
small-write allocation reduction is exact; large writes still pay for copied
partial ranges and byte-buffer construction.

Both native benchmark files occupy 83,073 bytes. Code grows from 41,552 to
42,344 bytes and unwind data from 7,276 to 7,492 bytes; data remains 4,536
bytes. The extra paths support raw input, range handling and byte buffering.
No size baseline changes are needed.

This enables byte-oriented consumers such as dircolors. It does not complete
their conversion or the remaining producer audit for #5714.
