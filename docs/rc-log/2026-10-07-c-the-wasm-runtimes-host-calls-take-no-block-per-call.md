# The wasm runtime's host calls take no block per call

2026-10-07: `wasm_ir.io_ret_addr`, the reactor's own poll buffers, the
`cabi_realloc` lend, and `std/serve`'s empty-read event. #11770.

## What the serve loop paid on wasm

`TestSelfHostServeAllocsPerRequest` read 0 per hello request on x86-64 and
arm64, and 8.29 on wasm over 10k requests. None of it was the loop's own
Fern code. Every host call the runtime makes through a preview-2 import took
blocks from the allocator for its own plumbing and gave them back after:

- the return area the host writes a stream result into, in
  `$__fern_tcp_recv`, `tcp_recv_into`, `tcp_send`, `tcp_send_bytes` and
  `tcp_send_buf`;
- the reactor wait's pollable list, its slot table and its return area,
  plus the ready list the host materialises through `cabi_realloc`;
- the bytes list `tcp_recv_into` was handed by the host and then copied
  into the caller's array;
- the wall clock's return area, once a second for the Date header.

Each was freed, so the leak census never saw them. The count gate does.

## The change

- **One static result record.** `io_ret_addr` is a 16-byte, 8-aligned
  record after `ip_box2_addr`. The stream verbs, the reactor wait and the
  preview-2 wall clock have the host write into it. It is dead before each
  body returns, and none of those bodies calls another while holding it.
- **The reactor owns its poll buffers.** `reactor_new` allocates the
  pollable list, the slot table and a ready buffer with the entry table, at
  room for 33 pollables. A wait that needs more grows all three to twice the
  entry table's capacity plus one. `reactor_ctl` op 3 frees them.
- **A lend through `cabi_realloc`.** A body that already holds room for a
  host call's result list sets `$__fern_lend` and `$__fern_lend_cap`. The
  first fresh request that fits takes the room and clears the lend, and the
  body clears it after the call. The reactor wait lends its ready buffer, and
  `tcp_recv_into` lends the caller's array, so the bytes land in place and
  the copy is skipped.
- **An empty read ends the event.** wasmtime reports a socket ready with
  nothing to read about once every twelve requests. The read returned
  `-EAGAIN` at once, and the loop parsed the unchanged buffer, found it
  idle, and boxed `__serve_unframed`'s five-tuple to say so.
  `__serve_event` now returns no position when its read brought no bytes,
  saw no end of stream, and the connection holds no backlog. A backlog event
  is synthetic and brings no bytes by design, which is what
  `TestWasmHTTPKeepAlive`'s pipelined leg caught when the first version left
  that out.

## Measured

`TestSelfHostServeAllocsPerRequest`, allocations per hello request.

| target | rounds | before | after |
|---|--:|--:|--:|
| x86-64 | 100k | 0 | 0 |
| wasm | 10k | 8.29 | 0 |

The wasm leg's residue is 28 allocations over 10k requests: the Date the
loop reformats once a second. It now runs as
`TestSelfHostServeAllocsPerRequestWasm`, pinned at 0.

`reactor_grow` in `TestSelfHostWasmComponentWaits` watches forty sockets for
both bits, so the first wait grows the buffers. A wait after the growth must
allocate nothing, which fails on the old runtime.

## Traps

- **wasmtime's poll returns a subset.** It returns once any pollable is
  ready, so one wait over forty writable sockets reported 23 and the next
  reported 40. A probe collects readiness across waits.
- **A read after a consuming call keeps the value shared.** The first
  version read `c.backlog[at]` after `__serve_read(c, …)` took `c`. That
  kept `c` alive, so the read's `.with` updates copied the connection arrays:
  5 allocations per request on x86-64. Reading the flag before the call
  restores 0.
