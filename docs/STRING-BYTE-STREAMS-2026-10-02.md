# Byte input for tee, head and tail

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
an intermediate array. The write helper borrows its input. A consuming
adapter releases flush's extracted buffer after the write returns.
The byte methods preserve the existing first-error behavior.

Two scan intrinsics support the migration. `__count_byte_bytes` counts
matching bytes in a borrowed array; invalid needles return zero.
`__rmemchr_bytes` finds the last match at or before a supplied index;
oversized indices clamp to the end, and negative indices or invalid needles
return -1. Both reuse packed native vector kernels and use bounded slot
scans for unpacked arrays. Neither allocates or constructs text.

## Validation checkpoint

This integration starts from raw-line checkpoint `59eb53e2c`. The compiler
source check, every lint gate, bootstrap Darwin and interpreter scan/output
tests, bootstrap core WebAssembly and Preview 2 tests, and host GNU
head/tail/tee comparisons pass.

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

Repeated-flush tests caught a leak when a fresh extracted array was passed
directly to the borrowing write helper. The consuming flush adapter fixes
that lifetime without changing the public borrowing contract. The existing
200-flush census passes on all three bootstrap targets; alias and sticky-error
cases also pass.

The pinned three-stage bootstrap, full unit tests and current performance
and size measurements remain pending. Historical prepared-branch measurements
are not evidence for this integration.
