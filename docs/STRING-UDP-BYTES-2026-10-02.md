# UDP byte sinks

Integration with `4278d1f9d` passes the refreshed Linux target matrix and all
lint gates, including the measured SSA admission census of 333 operations.
It includes the merged TCP sink and current formatter behavior. Bootstrap,
size and full-suite validation remain pending for this integration. The
completed measurements below describe the earlier checkpoint.

The current refresh integrates TCP checkpoint `788bac08c` and main
`c83a5855c`, preserving typed-IR-only lowering. Source checks, Linux target
tests and all lint gates pass. The pinned bootstrap reaches identical
stage-2 and stage-3 binaries of 12,197,089 bytes, SHA-256
`842842a28bc159277a74495ede99c0279ca8d7287fb03a59dbee824c2ff0e8df`.
The actual stage-2 compiler passes Darwin and WASI IPv4/IPv6 loopback probes.
Each native fixture records ten allocations, ten frees and zero live bytes.
WASI component execution verifies behavior. The refreshed full unit suite
and all lint gates pass from the immutable current source snapshot.

Building the TCP parent with the same stage-2 compiler gives 12,196,929 bytes,
so UDP adds 160 file bytes. Code grows from 10,471,136 to 10,480,256 bytes,
unwind data from 593,020 to 593,364, and data from 941,592 to 943,640.
These sections fit within the existing file segments; link-edit data grows
from 171,073 to 171,233 bytes. No size baseline was changed.

The earlier integration includes main through `0d7a8d321` and TCP byte-sink
checkpoint `b8881841c`. The pinned bootstrap reaches identical stage-2 and
stage-3 binaries of 12,131,025 bytes, SHA-256
`8a969e1658775f91d7be1f17a5a621d5b8cedc1697c4e5cd6188204d2ab328c0`.
Fresh stage-2 Darwin and WASI IPv4/IPv6 loopback probes pass, retaining the
allocation counts and fixture sizes below. Refreshed Linux targets and
`make lint-all` pass. The current full unit suite and all lint gates also
pass from the immutable final source snapshot.

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

## Validation checkpoint

The implementation is based on TCP byte-sink commit `d5fc77ab1`. The Linux
target matrix and `make lint-all` pass. The refreshed integration also
passes the full unit suite.

Real loopback tests verify exact 8193-byte payloads, retained and temporary
arrays, connected sends and empty datagrams over IPv4 and IPv6. They pass
with both primary native targets, both Go native backends, both WASI
components and the Go interpreter. Primary native runs require balanced
allocation counts. Library-only callers link and execute on both Linux
targets. Existing DNS, socket-control, IPv6 and text-UDP regressions pass.

The 56 WASI storage cases cover empty and 8193-byte arrays, both compilers,
resource handles zero and seven, and success plus every setup/send failure
stage. Each case repeats the operation 32 times, checks stable guest heap
usage and verifies resource cleanup. Primary core modules also require
equal allocation/free counts and byte totals.

The pinned seed produces identical stage-2 and stage-3 Darwin compiler
binaries of 12,114,481 bytes, SHA-256
`b57d3d2e1be8c1c3eb37343326c41514c079dbed0177f32172e5aaf3944c0c5c`.
Stage 1 differs because the seed predates generator changes on main.
That stage-2 compiler passes the loopback fixture on Darwin and Preview 2,
for both address families. Each Darwin fixture records ten allocations and
ten frees with zero live bytes. Preview 2 execution verifies behavior only.

## Measured cost

Both native allocation probes use the same stage-2 compiler and an 8192-byte
retained array containing ASCII bytes. A receiver verifies each datagram.

| Sink | One send | Sixteen sends |
| --- | ---: | ---: |
| Text conversion before each send | 10 allocations | 25 allocations |
| Byte array | 9 allocations | 9 allocations |

Every allocation was freed. With instrumentation removed, both one-send
executables occupy 33,185 bytes. These are allocation and size measurements;
no throughput claim is made.

The same stage-2 compiler built the TCP parent and UDP compiler sources.
Their executables occupy 12,097,809 and 12,114,481 bytes, a 16,672-byte
increase. Code grows by 9056 bytes, unwind data by 344 and data by 1792.
The text segment crosses a 16,384-byte boundary; link-edit data adds 288
bytes. The data segment's file size is unchanged. The added code implements
the two byte builtins and their target routing; the existing send bodies
are shared between text and byte variants. No size baseline was changed.

Repeating the compiler comparison on current main with the refreshed UDP
stage-2 compiler gives 12,130,865 bytes for its TCP parent and 12,131,025
for UDP, an increase of 160 bytes. Code grows by 9,064 bytes, unwind data
by 344 and data by 2,304; all fit in the existing file segments. Link-edit
data grows from 170,545 to 170,705 bytes. These measured layout differences
explain why file growth differs from the earlier checkpoint.
