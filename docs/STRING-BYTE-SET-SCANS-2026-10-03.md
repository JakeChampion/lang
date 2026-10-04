# Raw byte membership scans

`__scan_set_bytes(bytes, from, set)` returns the first member's offset, or
the input length on a miss. Negative starts clamp to zero. A nonzero table
entry denotes membership; bytes beyond the table's length are not members.
`__count_runs_bytes(bytes, inside, set)` counts transitions into membership,
with any nonzero `inside` carrying membership from the previous chunk.
Both operations borrow their arrays and allocate nothing.

Packed native arrays reuse the text scan loops with byte-array addressing.
Unpacked native arrays use bounded slot reads. Primary WASM reads packed
source and table payloads. The current integration uses IR IDs 361 and 367,
preserving published operation identities; lowering records both arrays as
borrowed operands. The original measurements below predate that integration.
The bootstrap implementations provide cross-target parity during migration.

The combined compiler reproduces itself from pinned stage0, with identical
stages 2 and 3 at 12,530,193 bytes, SHA-256
`73ad12ef8acdf6bc46f517107140c201b5ea250f3a7cf9a9c100e5847a3a7d79`.
Its final stage passes the shared oracle corpus on Darwin, core WASM,
Preview 2 and the interpreter. The corpus covers all byte values, short and
oversized tables, non-boolean entries, extreme starts, retained aliases and
chunk-boundary state. Compiled allocation probes report no scan allocations;
native and core WASM finish with 426 allocations and 426 frees, zero live
bytes. Preview 2 has no exit-time census, so its claim is limited to the
in-program allocation probe and behavior.

The same final compiler rebuilds the parent byte-for-byte. New scan
registrations, lowering, native/WASM emission and interpreter support add
9,352 bytes of compiler code, 384 bytes of unwind data and 1,024 bytes of
static data. The file grows 16,592 bytes, including segment alignment.
No size baseline is changed.

Linux target, registry, ownership, full unit and lint checks pass on the
combined record-processing snapshot. The native benchmark's disassembly
confirms that both text and raw scan loops remain inside its 64-iteration
outer loop.

An 8 KiB pilot passes before the same benchmark at 8 MiB. Two warmups precede
seven alternating samples. Input construction is outside the timed region;
each sample measures 64 scans. This workstream runs no other compiler or
test job during measurement, although other macOS services remain active.
Every text/raw sample range overlaps, so these results establish no speedup.

| Workload | Implementation | Median ms | Sample range ms |
| --- | --- | ---: | ---: |
| full set miss | Previous text | 100.346416 | 99.401875-101.551250 |
| full set miss | Raw bytes | 100.030375 | 98.850500-101.888625 |
| short set miss | Previous text | 270.535459 | 267.076750-285.829083 |
| short set miss | Raw bytes | 269.106458 | 266.870958-278.036083 |
| last byte | Previous text | 101.543417 | 100.806916-102.590542 |
| last byte | Raw bytes | 102.394375 | 100.407042-102.969792 |
| count runs | Previous text | 306.412625 | 294.321833-309.719250 |
| count runs | Raw bytes | 303.994625 | 296.806916-308.788250 |
