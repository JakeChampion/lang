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
