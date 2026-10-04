# Buffered readers return complete UTF-8 text

`LineReader` now keeps its input in a `ByteLineReader` and validates records
before returning strings. `next_line` accepts scalars split across physical
reads. `next_chunk` retains an incomplete trailing scalar and reads again
when needed, including when the requested chunk is smaller than that scalar.

Malformed text or a truncated final scalar stops the reader with
`InvalidUtf8("")`. An existing I/O error takes precedence. A valid partial
line preceding that failure can still be returned once. Both operations keep
unread bytes when callers switch between them, and returned strings remain
valid after later reads or closing the underlying reader.

Use `line_reader_new` to construct the cursor. Its public storage now wraps
the byte cursor. `ByteLineReader` retains its raw-byte contract; raw consumers
must migrate to it before this text boundary lands.

## Validation

The shared fixture tests lines, chunks and alternating calls with chunk sizes
1, 2, 3, 4, 7 and 4,096. It covers NUL, long records, every width of UTF-8
scalar, malformed and overlong encodings, surrogates, values beyond Unicode,
truncated tails and invalid prefixes beside incomplete suffixes. It checks
length-framed retained copies, exact line boundaries, valid text and sticky
errors. Interpreter tests inject partial I/O failures and verify that the
original error survives decoding failure.

On the final helper refactor, the Go interpreter/native/WASI matrix passes
in 194.736 seconds. The primary x86-64/ARM64/WASM matrix passes in 185.056
seconds, with balanced allocations and zero live bytes. The existing
`io_buffered` stdtest runs and passes on both Linux architectures. The raw
byte-reader regression also passes.

Pinned Linux bootstrap stages 2 and 3 match at 12,913,600 bytes, SHA-256
`67898227c33521bf74c3657b1c45a406008bbf325d5a88ac319f0c5a552f164b`.
Stage 1 differs; the pin is `stage0-20261001-c891ebc`. This matches the
unchanged compiler's prior fixed point.

Full Linux units and lint pass. The complexity gate passes without raising
its baseline. Pinned Darwin stages 2 and 3 match at 13,144,785 bytes, SHA-256
`85a30d6664eb5f7ddb1848e9658f212b5b91e2cf0c01bfe8c9d2bc92e9d598b5`.
Stage 1 differs. Darwin Go targets pass in 8.970 and 3.190 seconds; primary
native tests and the alternating-call interpreter case pass in 25.314
seconds. Native allocation checks balance with zero live bytes.

## Measurements

Before and after use the reproduced Darwin compiler above and differ only
in `std/io_buffered`. The program drains stdin through lines or chunks and
checks the total byte count. Seven-byte reads deliberately split scalars.
ASCII records are `plain text record` plus newline; Unicode records are
`é日本𐐀 text` plus newline. A 16-record pilot precedes 16,384 records,
changing only the record count. Each full workload reads 294,912 bytes.

Task-owned heavy jobs were idle during measurements; the desktop was not
isolated. Two warmups precede seven alternating samples. Census runs are
separate. Both versions balance allocations and report zero live bytes.

| Workload | Before median | After median | Before range | After range |
| --- | ---: | ---: | ---: | ---: |
| ASCII lines | 17.775 ms | 19.215 ms | 17.217-18.636 ms | 18.168-20.253 ms |
| ASCII chunks | 19.760 ms | 20.193 ms | 15.191-33.016 ms | 17.642-33.017 ms |
| Unicode lines | 16.184 ms | 17.054 ms | 15.724-16.611 ms | 16.538-18.926 ms |
| Unicode chunks | 14.988 ms | 19.793 ms | 14.592-15.517 ms | 19.488-20.368 ms |

The first three timing ranges overlap. Unicode chunks are slower in this
run: the new reader validates text and retains incomplete scalars instead
of returning strings split inside them. This is a correctness change, with
measured costs and no speedup claim.

| Workload | Before allocations | After allocations |
| --- | ---: | ---: |
| ASCII lines | 198,965 | 297,273 |
| ASCII chunks | 210,669 | 421,328 |
| Unicode lines | 198,965 | 297,273 |
| Unicode chunks | 210,669 | 552,403 |

The native benchmark file grows from 66,529 to 83,041 bytes. Code grows from
35,896 to 42,504 bytes, unwind data from 5,588 to 6,828 bytes, and data from
4,144 to 4,168 bytes. The text segment crosses a 16-KiB allocation boundary.
No compiler fixed-point size or size baseline changes.

Instruction and literal-pool counts reproduce both code sizes exactly:
raw reader helpers add 5,496 bytes, UTF-8 validation 1,480, scalar-boundary
helpers 696 and ownership helpers 864. Reworked text cursor functions remove
1,436 bytes, the main function removes 100, and legacy runtime code removes
404. Literal storage and alignment add the remaining 12 bytes. The added
code supplies the byte storage, validation and scalar carry used by this API.
