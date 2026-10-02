# DNS TCP queries retain their bytes

DNS now sends its length-prefixed TCP query through `tcp_send_bytes`.
The sender drains short writes, retries interrupted calls, and closes the
socket on a fatal error or zero progress. It returns the connected socket
only after the entire frame has been accepted. The caller retains its
original array. A zero-byte send before completion reports `NoReply` rather
than spinning or treating the incomplete query as sent.

The real UDP-to-TCP retry test also exposed a WebAssembly readiness bug.
`tcp_pollable` passed UDP socket handles to TCP's subscription API, which
the host rejected as the wrong resource type. Both compilers now dispatch
by the socket record's kind: established TCP connections subscribe to their
input stream, UDP sockets to their incoming datagram stream, and listeners
or pending TCP connects to socket-state changes. Bare or unknown record
kinds return -1. Imports follow the reachable operations.

The established TCP change also fixes receive deadlines. With a peer that
remains open and sends nothing, the previous stage-2 compiler's output
blocks beyond the two-second test watchdog. The corrected compiler returns
`None` for the ten-millisecond receive deadline. This is a semantic timeout
test, not a throughput measurement. Pollables are dropped before their
owning streams and sockets are closed.

## Validation

This refresh integrates main `da4dd43be`, including the checked text producers,
TCP and UDP byte sinks, bounded byte-read storage and `let` syntax. The pinned
Darwin bootstrap, including the release-entry CI fix described below,
reaches identical stage-2 and stage-3 binaries of 12,296,625 bytes, SHA-256
`bdd20b690a2d2b9e5c6c724138db0ebdf4832337cf674de6fb64f065bc0018ad`.
The seed is `stage0-20261001-c891ebc`.

The send-loop fixture extracts the production function and replaces only
connect, send and close calls. It checks exact remaining bytes and resource
cleanup for short success, interruption, failure before and after progress,
zero progress, connection failure and an empty frame. The actual stage-2
compiler passes these cases on Darwin and core WebAssembly, with 93 and 97
allocations respectively, all freed and zero live bytes. Its interpreter
also passes.

The imported DNS module passes a real UDP-to-TCP retry using actual stage-2
output on Darwin and WASI. Each run makes exactly two UDP and two TCP
queries, checks the length-prefixed TCP packets and verifies the address
and canonical-name answers. UDP includes an EDNS OPT record; TCP encodes
the original query without it. The native run records 499 allocations and
499 frees, with zero live bytes.

The deadline comparison executes WASI output from this stage-2 compiler
against a compiler built from parent `da4dd43be` using the same generator.
It checks the silent peer's connection cleanup as well as the result.

Before the release-entry CI fix, the same generator built the parent compiler
to 12,990,065 bytes and the DNS candidate to 12,990,081, an increase of
16 bytes. Code grew from
11,244,868 to 11,245,804 bytes, and data from 950,296 to 950,808 bytes.
The additional branches, import strings and emitted WAT select readiness
according to the socket kind. They fit in the existing segments; unwind
data remained 590,188 bytes. This comparison isolates the DNS change at
the published `4468195ad` checkpoint; it is not the final compiler size.
The subsequent [shared release-entry guards](RELEASE-ENTRY-GUARDS-2026-10-02.md)
remove repeated code and bring the x86 compiler within the unchanged size
gate. The final stage-2 DNS fault and network cases above have been rerun.

The refreshed Go and primary target suites, full unit suite, all lint gates
and async-fetch caller checks pass. The target matrix covers DNS exchange, paired
queries, NAT64, dial, send faults, TCP connect/stream/listener cases, receive
deadlines, reactors, SocketCtl/SocketV6 and WASI resource-lifetime tests.
The full suite will run in CI under the project's early-publication policy;
it remains a merge gate.

This is the DNS TCP consumer slice for #10948 under epic #5626.
The UDP byte migration is a separate slice. No whole-component leak claim
is made for the WASI network cases.
