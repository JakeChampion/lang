# Byte input for tee, head and tail

The October 3 publication refresh integrates main `4fab7fdaa`, including
merged raw-line PR #11207, the two rename operations and the retirement of
`dyn_downcast`. The byte scans use distinct IDs 358, 359 and 360.
The byte methods also use main's `BufBlock` lifetime: the last writer copy
frees the buffer. The fixture retains an alias through raw writes and
flushes, checks that both copies observe the same buffer, and relies on
automatic cleanup instead of manually freeing its handle.

`tee`, `head` and `tail` now read and write raw byte arrays. `head` and
`tail` also retain and scan bytes directly. Binary input, malformed UTF-8
and partial scalars no longer pass through strings in these paths. Options,
paths and diagnostics remain text. This advances #5714 within epic #5626.

The shared hold stores input blocks, a first-block offset and a 64-bit byte
count. Dropping a prefix clears consumed block references and compacts the
pointer array when at least half its entries are dead. Appending to a unique
hold transfers its block array into the owned append path. Long retained
records therefore avoid repeated payload copies. Short reads in head's
bounded seek helper assemble into one builder when needed.

`BufWriter.write_bytes` borrows its input and writes a full block directly
when the buffer is empty. `write_bytes_range` clamps ranges without creating
an intermediate array. The write helper borrows its input. The compiler
releases flush's extracted buffer after the write returns.
The byte methods preserve the existing first-error behavior.

Two scan intrinsics support the migration. `__count_byte_bytes` counts
matching bytes in a borrowed array; invalid needles return zero.
`__rmemchr_bytes` finds the last match at or before a supplied index;
oversized indices clamp to the end, and negative indices or invalid needles
return -1. Both reuse packed native vector kernels and use bounded slot
scans for unpacked native arrays. Primary WASM scans packed bytes directly.
Neither allocates or constructs text.

## Current validation

This integration bounds retained short reads and releases WASI seek scratch.
The opcode registry and SSA
admission drivers pass through the primary compiler: 337 registered
operations, with the same three unsupported operations as the parent.

The pinned bootstrap produces identical stage-2 and stage-3 binaries of
12,464,657 bytes, SHA-256
`ba5de9a62a57e90f34f4b658171bee275cc072b07955e4560dcc8460b33bf439`.
Stage 1 differs because the published seed predates generator changes.
The three builds take 34, 28 and 24 seconds. The reproduced stage-2 compiler
also passes the complete registry golden and lift-admission census exactly.

The actual stage-2 compiler passes the three byte-scan fixtures, the buffered
writer fixture, 124 head/tail cases and six tee cases across Darwin and core
WebAssembly. Every compiled probe balances its allocation census, and the
scan loops allocate nothing. All three scans also pass Preview 2 and the
primary interpreter; those runs establish behavior, not a component census.
The refreshed Linux target matrix and every lint gate pass from the immutable
source snapshot. The full unit suite passed at the earlier prepared checkpoint;
the publication integration still requires its full CI suite. Darwin's primary
byte-I/O tests, including the native rename operations, and GNU corpus pass
in 40.442 and 30.293 seconds. Primary utility parity passes in 31.852 seconds.

The integration corrects the prepared count/reverse WASM helpers' obsolete
four-byte element stride. Both now use packed byte offsets. Focused WASM
scans pass in 23.011 seconds, the Go target matrix in 30.256 seconds, GNU
parity in 23.786 seconds, and primary target/registry checks in 86.641 seconds.
The buffer-release and sanitizer regressions also pass in 28.387 seconds.
These durations are validation evidence, not performance comparisons.

The GNU corpus covers all byte values, LF/NUL records, long malformed
records, 8 KiB/64 KiB boundaries, multiple retained blocks, partial offsets,
zero and large counts, negative head counts, from-start tail counts, files,
pipes and tail-follow output. Buffered output checks direct and buffered
writes, clamped and inverted ranges, aliases and sticky errors. The scan
corpus checks every byte value, vector boundaries, extreme indices,
retained aliases and zero allocations across repeated compiled calls.

Repeated-flush and fresh-argument tests caught a missing ownership entry for
`buf_take_bytes` in the bootstrap compiler. That prerequisite now releases
fresh results after borrowing calls or discarded expressions. This covers
public byte writes as well as flush and removes the earlier consuming
flush adapter.

## Memory and timing

An earlier run exposed excessive retained capacity after short byte reads:
head used 251,379,712 bytes of peak resident memory on the long pipe case,
and tail used 152,059,904. Each short read pinned the full requested reserve.
The storage prerequisite keeps full reads in place and copies short reads
into compact arrays while retaining the source until the copy finishes.

The corrected integration was tested at 8192 input bytes, then at 8,388,608
bytes with only the input-size parameter changed. Six workloads cover
file and pipe input, byte and line selection, and long malformed records.
Untimed correctness runs match GNU coreutils 9.12 and uutils coreutils 0.12.0.
Two warmups precede seven timed rounds in alternating order, with output
sent to a sink. Peak resident memory is measured separately.

| Long-record pipe workload, 8 MiB | Before migration: peak resident bytes | Byte version: peak resident bytes |
| --- | ---: | ---: |
| head, all but the final line | 55,967,744 | 10,502,144 |
| tail, final line | 56,033,280 | 10,551,296 |

These are native Darwin measurements with instrumentation removed. The two
versions were built by the same final compiler, and all task-owned compiler
and test jobs had stopped. Desktop activity remained. Five timing sample
ranges overlap, including long-record tail: 41.448-75.153 ms before and
37.538-78.110 ms after. Long-record head ranges from 42.084-45.715 ms before
and 39.452-41.080 ms after. Those head ranges do not overlap in this run.
The evidence supports the memory reduction and this specific head timing
improvement, without a general throughput claim.

At the earlier prepared checkpoint, a separate instrumented tee probe reads a regular file through stdin and
writes identical arbitrary bytes to stdout and two files. Both versions
allocate and free 60 blocks at 8192 bytes, and 694 blocks at 8,388,608 bytes.
Both finish with zero live bytes. Pipe allocation totals are not compared
because read boundaries vary between executions.

## Measured size

The same final stage-2 compiler builds both sides. The comparison baseline
is main `4fab7fdaa`, built with the same compiler.

| Utility | Before file bytes | After file bytes | Code growth | Unwind growth | Data growth |
| --- | ---: | ---: | ---: | ---: | ---: |
| head | 132,913 | 149,425 | 4,668 | 1,016 | 0 |
| tail | 215,969 | 215,969 | 4,148 | 1,016 | 0 |
| tee | 116,353 | 116,353 | 1,860 | 432 | 0 |

Head crosses a 16,384-byte text-segment boundary and adds 128 link-edit
bytes. The added code implements retained block management, byte read and
range output, ownership cleanup and compact short reads. Tail and tee's
additions fit within their existing file segments.

The compiler grows from 12,464,529 to 12,464,657 file bytes. Code adds
11,864 bytes for the byte-scan operations and target routing, unwind data
adds 504, and data adds 2304. Both text and data file segments remain the
same size; link-edit data adds 128 bytes. No size baseline changed.
