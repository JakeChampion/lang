# HTTP byte serialization

The serializer and WASI byte writer are combined with the file-byte sink and
main's compiler changes at `68aa3984d`. Integrated target tests, the three-stage
bootstrap, full unit suite and lint pass. Timing measurements below retain their
original compiler provenance; artifact sizes use the current integrated compiler.

`http_serialize_response_bytes`, `http_serialize_response_conn_bytes`, and
`http_serialize_response_to_bytes` return the complete response as `u8[]`.
They preserve binary bodies, including malformed UTF-8 and NUL bytes, and
frame the bytes they actually emit. Chunk producers are evaluated once.

HEAD advertises the corresponding body length without emitting the body.
Informational, 204, and 304 responses neither evaluate nor emit the body.
File bodies retain the existing requirement to call `http_materialize`
first. The serializers themselves do not open files.

The existing string serializers retain their replacement-decoding policy.
Their Content-Length now describes that decoded representation. For example,
the body bytes `ff 41 80` become `ef bf bd 41 ef bf bd`: the old serializer
advertised 3 bytes but sent 7; the corrected serializer advertises 7. The
byte serializer sends the original 3 bytes and advertises 3.

TCP transport still uses its existing text interface. Switching it requires
a byte-capable socket sink; adding these serializers does not complete
#10948 or the string invariant in #5714.

## Assembly measurement

Three implementations of the same byte serializer were compiled with the
same primary compiler, based on the validated buffer-pruning change
`3d18b8940`. Each repeatedly serialized a 4096-byte valid Unicode body on
arm64 Darwin. Instrumentation was unset for timing. An eight-round run
verified the pipeline before increasing only the round count to 2048 and
then 32768. The final timing run started after the full host gate finished,
with no other compiler builds running in this workstream.
Nine alternating-order runs discarded the first two samples per variant.
All variants produced the same checksum.

| Assembly | Median for 32768 responses | Allocations per additional response |
| --- | ---: | ---: |
| Generic array concatenation | 221.330458 ms | 20 |
| Preallocated array with indexed writes | 197.638625 ms | 11 |
| Buffer builder with bulk copies | 28.907042 ms | 13 |

The implementation uses the builder. It costs two allocations more than the
indexed loop but copies the payload in bulk. Independent instrumented runs
at 8 and 16 rounds measured the allocation slopes above; all had matching
allocation/free counts and zero live bytes. The serializer checks that the
combined length fits `i32` before allocating the builder, and frees the
builder after extracting the owned result.

These are local process timings, including startup, for this workload.
The short eight-round ranges overlapped and do not establish a speedup.
At 32768 rounds, builder samples ranged from 17.180333 to 43.551084 ms,
below all indexed-loop samples (192.693625 to 325.368291 ms) and all concat
samples (211.5885 to 299.296041 ms). No emulated timing is used.

## Artifact size

A small response containing `aé𐐷z` was compiled with the same primary
compiler and executed on Darwin and core WebAssembly. All three versions
produced identical wire bytes.

| Serializer | Darwin executable | Core WebAssembly |
| --- | ---: | ---: |
| Previous text implementation | 99,873 bytes | 46,708 bytes |
| Corrected text implementation | 99,873 bytes | 46,505 bytes |
| Byte implementation | 99,841 bytes | 43,153 bytes |

The byte path removes UTF-8 validation/decoding helpers while adding the
builder lifecycle and bulk-copy helpers. Its core code section shrinks from
27,347 to 23,885 bytes and contains 166 functions instead of 179. The total
core reduction is 3,555 bytes. These are direct `core-module` outputs from
the integrated compiler, without the name section added by the earlier
WAT-to-binary measurement. There is no size baseline change.

## Validation

The shared fixture checks 21 exact responses across both interpreters and
compiled targets: all 256 byte values, Unicode, a stream cursor, captured
chunk producers, HEAD, bodiless statuses, empty and unmaterialized file
bodies, keep-alive, conflicting framing headers, and legacy text decoding.
It also verifies exactly twelve producer calls across the four responses
that need their three-step producer, including HEAD.

The integrated fixture passes both interpreters, Darwin ARM64, Linux ARM64
and x86-64, core WebAssembly, and Preview2 components for both compilers.
The primary component wrapper now supports the stderr-plus-exit combination
needed by the serializer's overflow assertion. Its prerequisite passes the
full unit/lint gate and a byte-identical three-stage Darwin bootstrap.

The actual stage2 compiler also passes the serialization fixture on Darwin,
core WebAssembly, and Preview2. Native reports 443 allocations and 443 frees;
core WebAssembly reports 494 and 494, both with zero live bytes. Components
are checked for behavior because their entry point does not print a census.
The integrated Linux target matrix passes, including both interpreters and
the real WASI HTTP host. The pinned seed produces identical stage-2 and stage-3
Darwin compiler binaries of 12,097,665 bytes, SHA-256
`c9046fac4eda687a09ee26b41a49a591373aa7e1056ee34ab6d38686381c65f0`.
Stage 1 differs because the seed predates generator changes on main.
The full unit suite and `make lint-all` pass in the updated isolated Linux snapshot.
Host Go linking
is blocked by Darwin file-table exhaustion; actual stage-2 execution covers
the integrated Darwin compiler.
