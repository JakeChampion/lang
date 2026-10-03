# Byte input for tee, head and tail

The October 3 refresh integrates raw-line checkpoint `d184de1f3`, including
checked stdin, compact short reads and the completed D8 implementation.

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
admission drivers pass through the primary compiler: 336 registered
operations, with the same three unsupported operations as the parent.

The pinned bootstrap produces identical stage-2 and stage-3 binaries of
12,513,393 bytes, SHA-256
`a9698db5138743ca3535219c827dc35e8e142b21c64657a335d352726cf9e4c1`.
Stage 1 differs because the published seed predates generator changes.
The three builds take 34, 26 and 23 seconds.

The actual stage-2 compiler passes the three byte-scan fixtures, the buffered
writer fixture, 124 head/tail cases and six tee cases across Darwin and core
WebAssembly. Every compiled probe balances its allocation census, and the
scan loops allocate nothing. All three scans also pass Preview 2 and the
primary interpreter; those runs establish behavior, not a component census.
The refreshed Linux target matrix, full unit suite and every lint gate pass
from the immutable final source snapshot.

The integration corrects the prepared count/reverse WASM helpers' obsolete
four-byte element stride. Both now use packed byte offsets. Focused WASM
scans pass in 25.062 seconds, the Go target matrix in 31.177 seconds, GNU
parity in 23.755 seconds, and primary target/registry checks in 84.999 seconds.
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
| head, all but the final line | 55,967,744 | 10,469,376 |
| tail, final line | 55,984,128 | 10,534,912 |

These are native Darwin measurements with instrumentation removed. The two
versions were built by the same final compiler, and all task-owned compiler
and test jobs had stopped. Desktop activity remained. All six timing
sample ranges overlap. For the long-record pipe cases, head ranges from
38.753-40.137 ms before and 37.856-61.911 ms after; tail ranges from
39.684-42.484 ms before and 36.438-40.775 ms after.
The evidence supports the memory reduction without a general throughput
claim.

A separate instrumented tee probe reads a regular file through stdin and
writes identical arbitrary bytes to stdout and two files. Both versions
allocate and free 60 blocks at 8192 bytes, and 694 blocks at 8,388,608 bytes.
Both finish with zero live bytes. Pipe allocation totals are not compared
because read boundaries vary between executions.

## Measured size

The same final stage-2 compiler builds both sides. The comparison baseline
is raw-line parent `d184de1f3`, built with the same compiler.

| Utility | Before file bytes | After file bytes | Code growth | Unwind growth | Data growth |
| --- | ---: | ---: | ---: | ---: | ---: |
| head | 132,913 | 149,425 | 4,900 | 1,024 | 0 |
| tail | 215,969 | 215,969 | 4,240 | 1,016 | 256 |
| tee | 116,353 | 116,353 | 1,888 | 432 | 0 |

Head crosses a 16,384-byte text-segment boundary and adds 128 link-edit
bytes. The added code implements retained block management, byte read and
range output, ownership cleanup and compact short reads. Tail and tee's
additions fit within their existing file segments.

The compiler grows from 12,496,737 to 12,513,393 file bytes. Code adds
11,168 bytes for the byte-scan operations and target routing, unwind data
adds 504, and data adds 2048. The text segment grows by 16,384 bytes;
link-edit data adds 272 bytes. No size baseline changed.
