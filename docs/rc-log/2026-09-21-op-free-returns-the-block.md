# op_free returns the block to its size class

2026-09-21 — `asm_ir`, `asm_arm64_ir`, `wasm_ir`, `irlower`.

## What was wrong

`__free(p, n)` — the raw deallocator `core/map` is written against — lowered to
`ir.op_free()`, and all three backends emitted that as a no-op: pop both
operands, push a dummy 0, keep the block. The comments called it "safe-leak
mode under the bump heap".

The heap has not been bump-only for a long time. `__fern_alloc` pops a
size-class freelist on every path (small tier indexed by words, large tier by
512 KiB class), and every rc release — `__fern_str_free`, `__fn___fern_arr_dec`,
`__fern_box_free`, the string builder's `buf_block_free` — pushes onto it. Only
the raw floor never did, so a block `__alloc` handed out could never come back.

Measured, x86-64, against native on the same source:

| | 5000 × (`__alloc(64)` + `__free`) |
| --- | --- |
| native | flat |
| self-host | the bump moves, every round |

## The fix

`__fern_raw_free(block, bytes)` per backend, beside `__fern_large_push`: round
the size the way `__fern_alloc` rounds the request, drop a block too small to
hold the freelist link, and push onto the class the allocator will pop from —
small tier inline, large tier through the existing helper. arm64 reuses
`buf_block_free`, which already released a bare block with no rc word; wasm
parks the successor in the bsz slot at `base+4`, as its other pushes do.

`op_free` calls it. The heap-event hook and the quarantine decline sit where
every other push has them, so `FERN_RC_FREE_DEBUG` and the sanitizer still
recycle nothing.

## The trap this had set

`__fern_arr_dec(v, stride)` and `__fern_drop_arr_ptr(v, stride)` were ALSO
lowered to `op_free` — not to free anything, but because op_free's pop-2/push-1
was a convenient spelling for a no-op. Making op_free real would have turned
those into a free of an rc-headered array box as a raw block of `stride` bytes:
`v` is not a block base and `stride` is not its size. That is heap corruption,
and the no-op was the only thing hiding it.

They keep today's behaviour and now spell it themselves, as two drops and a
dummy 0. Their real releases are a separate question — the element deep-drop
the stride selects has no self-host equivalent yet — but they no longer ride an
op that performs a free.

## What this does not reach

`core/map`'s insert still allocates where native does not, so a map leak bound
does not go green on this change alone. The cause is not the free: a
capture-free function value is a fresh box per pass in the self-host
(`__map_set_impl` forwards its hash and eq as two of them, so two allocations
per insert against native's zero), which is #9839. This makes those boxes
reclaimable; it does not stop them being allocated.
