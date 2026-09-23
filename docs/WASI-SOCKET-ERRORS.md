# WASI socket errors

The existing integer TCP and UDP APIs return a negative Preview 1 errno for
errors from `wasi:sockets/network@0.2.0`. The host returns an `error-code`
discriminant, which must be converted before negation: `unknown` is zero.
Negating it directly previously reported success.

`internal/strerror.WasiSocketErrorCodes` defines the mapping, in the order of
the [upstream WIT enum](https://github.com/WebAssembly/wasi-sockets/blob/v0.2.0/wit/network.wit).
The bootstrap uses a Fern runtime function; the self-host emits the same
mapping from its pinned table. Invalid host literals return `-EINVAL` on both
Wasm compiler paths. Stream errors remain the existing separate `-1` result;
this conversion applies to socket `error-code` results.

The mapping preserves the named address, connection, timeout and readiness
conditions. WIT combines some native conditions, so the conversion cannot
recover their original detail:

| WIT case | Preview 1 errno |
| --- | --- |
| unknown or out-of-range discriminant | EIO |
| not-in-progress or invalid-state | EINVAL |
| new-socket-limit | EMFILE |
| remote-unreachable | EHOSTUNREACH |
| name-unresolvable | ENOENT |
| temporary-resolver-failure | EAGAIN |
| permanent-resolver-failure | EIO |

These are the legacy integer APIs. The closed `NetError` API, owned socket
resources and nonblocking reactor remain P0 work under #9853.

Setup failure now drops any socket created by that operation. UDP send errors
also drop the datagram streams before their parent socket. Failed socket
creation owns nothing and does not drop a handle; successful TCP setup transfers
the socket and streams to its caller. The cleanup treats handle zero as valid.

Fault-injection gates run the actual compiled socket bodies with a host stub
that tracks ownership and traps on double drops or dropping a parent first.
They cover every listen/connect/UDP setup stage, both handle zero and a nonzero
handle, UDP send failures, and successful ownership transfer. They measure host
socket resources only. Guest-memory scratch reclamation and the worker-owned
network capability remain separate P0 work.

TCP listener and connection records use 16 bytes: socket, input stream, output
stream, then a streams-present word. Listeners clear that word; connect and
accept set it. Close uses this word to distinguish absent streams from live
streams with handle zero, then drops the socket unconditionally. This private
layout is shared by both compilers and does not change the integer API.
Close fault tests cover listeners, outbound connections and accepted connections
with zero and nonzero handles, including setup failures before close.

Listen, connect and accept reuse their 16-byte canonical return area as the
successful socket record. Close returns it to the allocator after dropping
the resources. A setup error saves its errno before freeing the return area:
the freelist can overwrite the former error payload. Repeated fault probes
check one allocation per operation and a flat heap high-water mark after
warmup. UDP and stream-I/O scratch, and the worker's network capability,
remain separate work.

Local-port queries also release their canonical return area after saving
the port or errno. The socket remains borrowed throughout the lookup.

Real WASI loopback lifecycle tests also check the allocator census. Across
32 listen/local-port/connect/accept/close cycles, both compilers report
128 allocations, 128 frees and zero live guest bytes. Stream I/O and UDP
are outside this measurement.

UDP now releases its parse/return scratch, outgoing-datagram record and any
temporary inline-string spills on success or error. Heap-form strings remain
borrowed. Error values are saved before returning scratch to the freelist.
Repeated fault probes cover setup and send failures, malformed hosts and all
payload forms; real loopback tests verify payload bytes and zero live guest
bytes. Stream I/O scratch and worker-owned network capabilities remain pending.
The self-host test gives the external Preview 1 adapter separate stack pages;
its persistent stacks are outside the Fern heap census, not reclaimed by UDP.

TCP send releases its return area and any temporary inline-string spill after
the final host read, including empty sends and mid-stream errors. The return
area holds the full twelve-byte `result<_, stream-error>` layout. A
`last-operation-failed` payload owns an `io/error` resource, which send drops
even when its handle is zero; `closed` owns no error resource. The public
integer result remains the byte count or `-1`.

TCP receive copies the canonical returned byte list into its owned Fern array,
then frees the list and return area. Empty lists own no allocation. Closed
streams and other stream errors still return an empty array; owned error
resources are released first. Nonpositive read limits return an empty array
without calling the host in both compilers. Raw bootstrap core modules using
receive now export `cabi_realloc`, so an external host can supply returned bytes.
