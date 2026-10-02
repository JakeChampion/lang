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

This refresh includes the merged TCP sink, formatter changes and opcode
census through `4278d1f9d`, followed by byte-storage parent `6bc8a2961`.
The first integration passes the Linux target matrix and all lint gates.
Its measured SSA census admits 330 of 333 registered operations; the three
existing unsupported operations remain unchanged.

The final storage integration reaches identical stage-2 and stage-3
compiler binaries of 12,395,313 bytes, SHA-256
`877f1059c6f14ce3b9af3d708023f1ec31e140468aa2021609ca0b8508ae9bf7`.
The seed is `stage0-20261001-c891ebc`. Stage 1 differs because that seed
predates generator changes.

The actual final stage-2 compiler passes Darwin and WASI IPv4/IPv6 loopback
probes. Each native fixture records ten allocations, ten frees and zero
live bytes. WASI component execution verifies behavior. Final integrated
Linux targets, the full unit suite and all lint gates pass.

## Target and failure coverage

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

The same final stage-2 compiler builds the byte-storage parent and UDP
compiler sources:

| Section | Parent bytes | UDP bytes | Growth |
| --- | ---: | ---: | ---: |
| Executable file | 12,362,129 | 12,395,313 | 33,184 |
| Code | 10,640,436 | 10,649,564 | 9,128 |
| Unwind data | 595,276 | 595,620 | 344 |
| Data | 949,272 | 951,320 | 2,048 |

Code and data each cross a 16,384-byte file-segment boundary; link-edit data
adds 416 bytes. Those changes account for the entire file increase. The
added code implements the two byte builtins and their target routing;
existing send bodies are shared between text and byte variants. No size
baseline was changed.

These figures supersede earlier measurements on older compiler parents.
