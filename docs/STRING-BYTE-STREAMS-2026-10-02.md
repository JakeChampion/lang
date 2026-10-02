# Byte input for tee, head and tail

The timing pass exposed excessive retained capacity after short native byte
reads. A compact short-read path is now under validation. The completed
gates and size figures below describe the preceding version and must be
refreshed before publication.

The completed 8,388,608-byte pipe probe measured head's peak resident memory
at 55,984,128 bytes before migration and 251,379,712 after it; tail measured
56,000,512 and 152,059,904 bytes. Partial reads retained each request's full
allocation. The pending runtime fix keeps full reads in place and copies
partial reads into compact arrays. A borrowed helper keeps the source array
alive until copying finishes.

A deterministic probe retains 64 reads of 1024 bytes while requesting 65536
bytes each time. Allocator growth fell from 5,243,992 to 150,616 bytes; the
fixed probe records 267 allocations and 267 frees. The host regression test
passes with full reads, short reads and sanitizer quarantine. Linux coverage,
bootstrap, final benchmarks and the full suite still need to validate this
fix. The memory regression above is not an accepted publication result.

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
scans for unpacked arrays. Neither allocates or constructs text.

## Validation checkpoint

The current refresh integrates main `c83a5855c` and its AST-lowering
retirement. Source checks, all lint gates, bootstrap target tests, GNU
head/tail/tee parity and the primary Linux/WebAssembly matrix pass. Fresh
builder arguments are covered in both compilers, including direct, buffered,
ranged, empty and sticky-error writes. The scan tests run the production
typed-IR path once per target, with interpreter coverage retained. The full
unit suite and every lint gate pass from the immutable current source
snapshot preceding the compact-read fix. Final timing measurements remain
pending after the regression described above.

The pinned bootstrap produces identical stage-2 and stage-3 binaries of
12,213,537 bytes, SHA-256
`5d3f520c366e619fd00cc72ae2aee8e8f0d3609029f4609a2c83f5264ad2fbc1`.
Stage 1 differs because the published seed predates generator changes.
The actual stage-2 compiler passes the three byte-scan fixtures, the buffered
writer fixture, 124 head/tail cases and six tee cases across Darwin and core
WebAssembly. Every compiled probe balances its allocation census, and the
scan loops allocate nothing. All three scans also pass Preview 2 and the
primary interpreter; those runs establish behavior, not a component census.

The GNU corpus covers all byte values, LF/NUL records, long malformed
records, 8 KiB/64 KiB boundaries, multiple retained blocks, partial offsets,
zero and large counts, negative head counts, from-start tail counts, files,
pipes and tail-follow output. Buffered output checks direct and buffered
writes, clamped and inverted ranges, aliases and sticky errors. The scan
corpus checks every byte value, vector boundaries, extreme indices,
retained aliases and zero allocations across repeated compiled calls.

The refreshed Linux matrix passes bootstrap native, SSA, interpreter and
WebAssembly probes, GNU head/tail/tee parity, and primary x86-64, ARM64 and
core WebAssembly tests with balanced allocation censuses. The IR registry
and constructor checks and every lint gate pass.

Repeated-flush and fresh-argument tests caught a missing ownership entry for
`buf_take_bytes` in the bootstrap compiler. Its runtime produces an owned
array, but call-temporary reclamation did not recognize that result. The
compiler now releases it after a borrowing call or discarded expression.
This covers public byte writes as well as flush and removes the earlier
consuming flush adapter. Fresh-argument and 200-flush WebAssembly censuses
pass, as do the refreshed native and primary target checks.

## Measured size

The same final stage-2 compiler builds both sides. The baseline is the
raw-line parent `59eb53e2c` integrated with the same main `c83a5855c`.

| Utility | Before file bytes | After file bytes | Code growth | Unwind growth | Data growth |
| --- | ---: | ---: | ---: | ---: | ---: |
| head | 132,913 | 149,425 | 4,328 | 960 | 0 |
| tail | 215,969 | 215,969 | 3,660 | 952 | 256 |
| tee | 116,353 | 116,353 | 1,348 | 368 | 0 |

Head crosses a 16,384-byte text-segment boundary and adds 128 link-edit
bytes. The assembly-object comparison identifies 1,276 bytes for
`hold_drop`, 720 for `hold_push`, and the byte-array read/range and ownership
helpers. These implement bounded block retention, prompt release of consumed
blocks and byte output. Existing `copy_elide_lines` and `copy_elide_bytes`
shrink by 852 and 456 bytes in that comparison. Tail and tee's additions fit
within their existing file segments.

The compiler grows from 12,196,881 to 12,213,537 bytes. Code adds 11,200
bytes for the byte-scan operations and target routing, unwind data adds 504,
and data adds 2,048. The text segment grows by 16,384 bytes and link-edit by
272; the data segment's file size is unchanged. No size baseline changed.

Timing measurements are pending a quieter host. Historical prepared-branch
measurements are not evidence for this integration.

An instrumented tee probe reads a regular file through stdin and writes the
same arbitrary bytes to stdout and two files. At 8,192 input bytes, the
baseline and byte version allocate 60 and 59 times; at 8,388,608 bytes both
allocate 694 times. Every allocation is freed. This establishes allocation
parity for the larger controlled input, not an allocation or throughput win.
Pipe-input allocation totals are not compared because read boundaries vary
between executions.
