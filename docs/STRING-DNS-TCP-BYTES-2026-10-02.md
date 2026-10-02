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

The send-loop fixture extracts the production function and replaces only
connect, send and close calls. It checks exact remaining bytes and resource
cleanup for short success, interruption, failure before and after progress,
zero progress, connection failure and an empty frame. Both compilers pass
on Linux ARM64, x86-64 and core WebAssembly, along with both interpreters.
Primary native and core runs require balanced allocation censuses.

The imported DNS module passes a real UDP-to-TCP retry with both compilers
on Linux ARM64, x86-64 and WASI, plus the Go interpreter. Primary native
runs require balanced allocations. The existing DNS exchange, paired query,
NAT64 and dial regressions pass. WASI TCP connect, stream and listener tests,
socket subscriptions, polling storage and lifetime tests, and reactor tests
also pass, as do the Go WebAssembly backend unit tests and `make lint-all`.
The additional checked-in silent-peer deadline test passes with both
compilers, and its refreshed lint gate passes. The full unit suite and all
lint gates also pass from the immutable final source snapshot.

The pinned three-stage Darwin bootstrap reaches identical stage-2 and
stage-3 compiler binaries of 12,130,881 bytes, SHA-256
`c4a03680a66097fdb62a5541f3f72ed8cd45805d4c9d5a2aada9853359ff56de`.
The deadline comparison above executes this actual stage-2 compiler's
WASI output against the previous TCP checkpoint's output.

The same stage-2 compiler builds the parent compiler to 12,130,865 bytes
and this candidate to 12,130,881, an increase of 16 bytes. Code grows by
936 bytes and data by 512, fitting in the existing segments; unwind data
is unchanged and link-edit data grows by 16 bytes. No size baseline changes.

This is the DNS TCP consumer slice for #10948 under epic #5626.
The UDP byte migration is a separate slice. No whole-component leak claim
is made for the WASI network cases.
