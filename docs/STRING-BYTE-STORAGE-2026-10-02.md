# Compact byte reads and temporary-array ownership

Short byte reads retained the full requested allocation. Keeping many small
chunks from a pipe could therefore reserve much more memory than their payload.
Full reads still return their existing storage. Partial reads now allocate from
the actual byte count and release the original reserve after copying.

The primary native runtime uses a borrowed helper to keep the source array alive
through the copy. Primary WebAssembly expands bytes into compact four-byte
slots. Bootstrap native and Preview 1 backends compact their packed arrays;
Preview 2 already allocated from the host list's actual length.

Repeated-read coverage also exposed unreleased WASI seek return buffers. Both
compilers now release Preview 1 seek scratch on success and failure. Bootstrap
Preview 2 releases its seek return and stat buffers, and rejects a seek after
close before borrowing a dropped host resource.

Separately, the bootstrap compiler now recognizes `buf_take_bytes` as returning
an owned reference when reclaiming temporary call results. Fresh borrowed
arguments and discarded results are released after use.

## Validation

The retained-read fixture holds 64 chunks of 1024 bytes, then checks every byte.
It covers full requests, 65536-byte requests, sanitizer quarantine, invalid seek
modes and closed descriptors. Without quarantine it bounds allocator growth;
with quarantine it checks contents and reclamation. The primary compiler must
finish with balanced allocations and zero live bytes.

Bootstrap Reader and IoError objects still use documented immortal headers.
Its test compares the same program with zero and 64 reads, requiring no extra
retained blocks or bytes. The measured host controls retain 3 blocks/112 bytes
on Darwin, 5/112 on Preview 1 and 7/160 on Preview 2. These are fixed-retention
controls, not claims of a balanced whole-program bootstrap heap.

Darwin, both Linux architectures, core WebAssembly and bootstrap Preview 2
tests pass. Coverage also includes fresh builder-result censuses, existing byte
builders, byte readers, byte pipelines, seek behavior, and explicit refusal of
the primary component's unsupported reader API. The final full unit suite and
every lint gate pass, including a fresh retained-read and seek check after the
runtime dependency update. Existing buffered-writer and byte-pipeline tests
also pass with the fresh-result ownership change.

The pinned bootstrap reaches identical stage-2 and stage-3 binaries of
12,362,129 bytes, SHA-256
`0a42ac6f6559fd55864b072ffd5eed3bd210609818323ac5174ef9c793b9aebb`.

## Measured retained storage

The actual stage-2 compiler builds both the parent compiler source at
`4278d1f9d` and the candidate. Both drivers compile the same instrumented
retained-read program. A one-read pilot passes before scaling only the read
count to 64. These are allocator measurements, not timing claims.

| Target | Parent growth, 64 reads | Fixed growth, 64 reads |
| --- | ---: | ---: |
| Darwin ARM64 | 5,243,992 bytes | 150,616 bytes |
| Core WebAssembly | 20,972,632 bytes | 655,968 bytes |

The parent exceeds the storage bound on both targets. Its WebAssembly run also
leaves 512 bytes in seek scratch. The fixed native probe records 270 allocations
and 270 frees; WebAssembly records 336 and 336. Both finish with zero live bytes.
The one-read pilot incurs one extra compacting allocation, as expected.

Using the same compiler for both sources, compiler file size rises from
12,362,081 to 12,362,129 bytes. Mach-O code grows by 888 bytes and data by 768;
unwind data and segment allocations are unchanged. The added runtime generators
and cleanup fit existing segment padding. No size baseline changes.
