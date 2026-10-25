# 2026-10-25 — a lent byte slice is a window on wasm, not a copy (#13207)

`wasm_ir.frame_window_wat`, `ir.view_string`. Follows #12780, which held a
lent byte slice's descriptor in three frame slots on x86-64 and arm64.

## Why wasm copied

The native frame form, `ir.op_raw_string_frame`, writes a `{-1, data, len}`
string box into the frame and hands the callee its address tagged 2. That
works because a native string is a descriptor with a data pointer: every
string-arm reader of a view (`ssabytes`'s decode, length and data, the
crc32, sum, bsd, mismatch and string-copy kernels) reads the bytes through
it. Wasm linear memory has pointers too, but two things were missing:

- a wasm string is an inline `{len, bytes}` block, so a string box cannot
  point at bytes that live somewhere else;
- a callee cannot address its caller's locals, and the backend had no
  linear-memory frame to put a descriptor in.

So the wasm form of the op called `$__fern_str_borrow`, a fresh block and
a copy of the bytes, on every call.

## The window

A module with a frame-form `raw_string` gets a shadow stack: 1 MiB between
the free lists and the heap, `$__fern_shadow_sp` growing down to
`$__fern_shadow_lo`. A function with K such ops reserves 8K bytes on entry,
traps if that passes the low end, and gives the bytes back before each
`return` and at the end of a block wrapping its body. The op stores
`{data, len}` at its eight bytes and leaves the address plus 1, so the view
the callee sees is tagged 3. The frame base is read from the global, not
held in a local: every callee pops what it pushed.

Every reader of a view's string arm tells a window (odd address) from a
string: the decode, length and data reads carry `ir.view_string`, which only
wasm acts on, and the wasm kernels that take a string (`strcat`, `mismatch`,
`crc32_cksum`, `sum_bytes`, `bsd_sum`) and the ones that take a whole view
(the writer, `bytes_owned`/`range`/`into`, the AES view reads) decode it.
The rc helpers already return early on an odd address, so the frame's
release of the view does nothing, as it does for the native frame box.

## Measured

Allocations per 1,000 calls, wasm32-wasi:

| Row | Before | After |
|---|---:|---:|
| #13207's `count(buf[0:8])` | 1,000 | 0 |
| `TestSelfHostLentViewLetsItsRecordMove`, through the field and through a local | 1,000 / 1,000 | 0 / 0 |
| `TestSelfHostRecordPathAllocs`, `viewlen(pt[1:90])` | 1,000 | 0 |
| `TestSelfHostLentWindowConsumers`'s loop, five lent slices a round | 5,000 | 0 |

x86-64 and arm64 are 0 before and after. A recursion lending
`d[1:d.len()]` at every level reaches the same depth under wasmtime's
default stack as before (10,000 levels, not 11,000); with
`-W max-wasm-stack` raised it reaches 130,000 and traps at the shadow
stack's end at 140,000.

## Traps

Holding the frame base in a local cost a recursion a fifth of its depth
(8,000 levels): the local is live across every call, so Cranelift keeps it
in the native frame.

`watbin`, which assembles the flat form, takes no `offset=` on a load or a
store; `-emit asm` and `wasmtime run` on the text accept it, so only a
binary build shows it.

## Left

A string slice's frame form (`op_str_slice_frame`) still copies on wasm
(`$__fern_substr`): its value is a string, which every string op reads
inline. A byte slice's heap form still copies too (the 60,000-byte copy
`TestSelfHostByteViewRuntime`'s `sub-range-zero-copy` pins at exit 7): a
heap window would need a release that frees it, and the rc helpers skip
an odd address.
