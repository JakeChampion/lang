# UDP byte sinks

`udp_send_bytes(host, port, data: u8[])` and
`udp_sendto_bytes(fd, addr, port, data: u8[])` send one complete datagram.
They preserve arbitrary bytes and send empty arrays as empty datagrams.
The payload and address are borrowed. The one-shot form accepts an IPv4
literal; the descriptor form supports IPv4, IPv6 and an empty address for
the connected peer. Existing text builtins keep their contracts.

`std/net.send` and `send_to` now accept byte arrays. DNS sends its UDP packet
directly, without constructing an unchecked string. Text callers can import
`std/string` and pass `text.bytes()`.

The primary native compiler borrows packed payload storage. Its diagnostic
word-array path uses owned packing scratch. The Go bootstrap native and
WebAssembly backends borrow packed arrays. Primary WebAssembly packs its
word-sized slots into one buffer, then frees that buffer after success or
failure. It does not split the payload into multiple datagrams.

Both checkers, all four capability inventories, native and WASM backends,
runtime roots and the Go interpreter support the new builtins. Primary
interpreter networking remains unsupported.

## Current integration

This refresh includes main through `9b3b60c32`, integrated into the UDP
branch by `7681b3e75`. It includes the TCP byte sink, bounded byte-read
storage and upstream interpreter fixes. Refreshed Linux targets, the full
unit suite and all lint gates pass.

The final storage integration reaches identical stage-2 and stage-3
compiler binaries of 12,081,729 bytes, SHA-256
`9b5f04793cf017f7fce2f8760f0257489cbad849230c9b2b85c6d2d58a133367`.
The seed is `stage0-20261001-c891ebc`. Stage 1 differs because that seed
predates generator changes.

The actual final stage-2 compiler passes Darwin and WASI IPv4/IPv6 loopback
probes. Each native fixture records ten allocations, ten frees and zero
live bytes. WASI component execution verifies behavior. The actual stage-2
interpreter also passes HTTP byte serialization, matching native output
exactly, and the nullary and shadowed-variant regression cases.

## Target and failure coverage

The interpreter regressions cover a nullary IoError variant and a user enum
that reuses `BodyBytes`. The user variant must dispatch through its declared
owner even when a method on the builtin Body appears first. Upstream now
derives enum method aliases from variant declarations, including injected
builtins; the HTTP serialization case exercises this through the stdlib.

Real loopback tests verify exact 8193-byte payloads, retained and temporary
arrays, connected sends and empty datagrams over IPv4 and IPv6. The target
matrix covers both primary native targets, both Go native backends, both
WASI components and the Go interpreter. Primary native runs require
balanced allocation counts. Library-only callers link and execute on both
Linux targets. Existing DNS, socket-control, IPv6 and text-UDP regressions
remain in the matrix.

The 56 WASI storage cases cover empty and 8193-byte arrays, both compilers,
resource handles zero and seven, and success plus every setup/send failure
stage. Each case repeats the operation 32 times, checks stable guest heap
usage and verifies resource cleanup. Primary core modules also require
equal allocation/free counts and byte totals.

## Measured cost

Both native allocation probes use the final stage-2 compiler and an
8192-byte retained array containing ASCII bytes. A receiver verifies each
datagram. The one-send pilot passed before scaling to sixteen sends.

| Sink | One send | Sixteen sends |
| --- | ---: | ---: |
| Text conversion before each send | 10 allocations | 25 allocations |
| Byte array | 9 allocations | 9 allocations |

Every allocation was freed. With instrumentation removed, both one-send
executables occupy 33,185 bytes. These are allocation and size measurements;
no throughput claim is made.

The same final stage-2 compiler builds main parent `9b3b60c32` and UDP
compiler sources:

| Section | Parent bytes | UDP bytes | Growth |
| --- | ---: | ---: | ---: |
| Executable file | 12,065,057 | 12,081,729 | 16,672 |
| Code | 10,349,076 | 10,358,196 | 9,120 |
| Unwind data | 577,340 | 577,684 | 344 |
| Data | 951,064 | 953,112 | 2,048 |

Code crosses a 16,384-byte file-segment boundary, data fits in its existing
segment, and link-edit data adds 288 bytes. Those changes account for the
entire file increase. The
added code implements the two byte builtins and their target routing;
existing send bodies are shared between text and byte variants. No size
baseline was changed.

These figures supersede earlier measurements on older compiler parents.
