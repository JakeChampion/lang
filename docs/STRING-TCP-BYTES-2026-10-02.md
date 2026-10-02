# TCP byte sink for D9

Integration checkpoint, October 2: target tests and lint pass with main
through `0d7a8d321`. The pinned bootstrap produces identical stage-2 and
stage-3 binaries of 12,130,865 bytes, SHA-256
`e5f410b9da8852a06b1a586c4551a9c79f92012db8fc2b79952ac760519c7fba`.
Actual stage-2 Darwin loopback passes with ten allocations and ten frees;
native descriptor and SIGPIPE probes use zero allocations. The WASI component
loopback passes. The full unit suite passed on the earlier `d5fc77ab1`
checkpoint; the current main integration still needs that full gate.
Measurements below retain their original source provenance.

`tcp_send_bytes(fd: i32, data: u8[]): i32` borrows an owned byte array and
sends its bytes without constructing a string. Native code returns the
underlying send count or negative errno, including short counts. It suppresses
SIGPIPE for the call without changing the process's signal disposition.

The primary native implementation lends packed array storage directly.
Its diagnostic word-array form packs owned scratch. Primary WebAssembly
packs word-sized array slots into scratch bounded at 4096 bytes; the Go
bootstrap already has packed arrays and lends their storage. WASI writes
use chunks of at most 4096 bytes. If a later chunk fails, the result includes
the bytes from earlier successful chunks; failure before any successful
chunk returns -1. Both implementations release owned stream errors, including
handle zero. The primary implementation also releases its packing scratch.

Both checkers, capability inventories and compiler backends know the builtin.
The Go interpreter supports it. Primary interpreter networking remains
unsupported. This slice does not change the existing text-send contract or
add byte-view arguments.

## Validation checkpoint

On the file-byte parent `83df547b1`, the Linux target matrix and lint passed.
The real loopback fixture sends 8193 bytes cycling through all byte values,
reuses a retained array, sends a temporary array containing invalid UTF-8,
and checks empty input. It passed on both primary native targets, both Go
native backends, both WASI components and the Go interpreter. Primary native
runs require balanced allocation counts. Native tests also exercise invalid
descriptors and repeated writes after shutting down a socket's write half.

All 64 deterministic WASI fault cases passed. The oracle checks exact payload
bytes and chunk limits, failure on each chunk, counts from earlier successful
chunks, and release of owned error handles. It covers empty and 8193-byte
arrays, both error variants, handles zero and seven, and both compilers.
The primary cases start from its direct core-binary output.

The published-seed Darwin bootstrap reached identical stage-2 and stage-3
binaries of 12,097,809 bytes, SHA-256
`2f6403c1316982229188722a565546e4d100780bd5e63d11c4c1e22c61210b20`.
That stage-2 compiler passed the loopback fixture on Darwin with ten
allocations and ten frees, and on WebAssembly composed with the standard
WASI adapter. Its native invalid-descriptor and SIGPIPE probes each recorded
zero allocations. Component behavior is not evidence of whole-component
allocation balance. The primary CLI's default component wrapper does not
support socket imports, so the component check uses direct core output and
the same adapter composition as the checked-in target test.

After integrating the file-byte per-module fix and newer CI changes from
`7052f1c0c`, the target matrix and lint pass again. Library-only file and TCP
callers link and execute on both Linux targets; the TCP fixture checks an
invalid descriptor and continued access to its borrowed array. The refreshed
bootstrap reaches the same stage-2/stage-3 hash. The full unit suite and
`make lint-all` pass on the final `d5fc77ab1` source snapshot.

## Measured native allocation cost

Both sides use the same stage-2 compiler and an 8192-byte retained array of
ASCII bytes. The text case constructs a valid string before each send; the
byte case passes the array. A receiver checks every byte. Increasing the
number of sends from one to sixteen changes total allocations as follows:

| Sink | One send | Sixteen sends | Allocations per additional send |
| --- | ---: | ---: | ---: |
| Text | 10 | 25 | 1 |
| Bytes | 9 | 9 | 0 |

Every counted allocation was freed. With sanitizer and census settings
removed, both one-send Darwin executables occupy 33,185 bytes. These are
allocation and artifact measurements, not throughput measurements.

The same stage-2 compiler built both compiler sources. The file-byte parent
occupies 12,097,665 bytes; the TCP candidate occupies 12,097,809, an increase
of 144 bytes. Mach-O inspection shows 6000 additional code bytes, 168 unwind
bytes and 2816 data bytes. These fit in existing file-segment padding; the
144-byte file growth is link-edit data. The data segment's virtual size grows
by 16,384 bytes. This accounts for the new builtin routing and runtime
generators without changing a size baseline.

This supplies one sink required by #10948 and #5714 under epic #5626.
HTTP and DNS consumers still need byte migration, and UDP needs a byte API.
