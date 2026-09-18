# The self-host heap has no slack to append into

Null result. Porting native's in-place string append (`__fern_str_append`,
#5637) to the self-host compiler does not pay off on its own, and a naive
lowering makes the common case **14x slower**. Both causes are below the
append helper, so the port has to wait for them.

Native hit the first of the two already, and fixed it: #8404 is where its
append stopped testing "same 16-byte class" and started asking the allocator's
own class function, which is the whole difference between copying the
accumulator every time and amortised O(1).
`2026-09-05-str-append-class-capacity.md` is that entry, and it is the model
for the self-host port below rather than something to re-derive. (Native's
arm64 has no in-place append at all — #8414.)

## What was measured

An x86-64 self-host compiler with an in-place `__fern_str_append` helper,
against the same compiler without it — same lowering path (`FERN_SEM_IR=`),
same sources, so the helper is the only variable. Benchmarks append a
DIFFERENT piece each round; see the trap at the bottom for why that matters.

| benchmark | accumulator | base | with helper |
|---|---|---|---|
| 20 000 appends | grows to 1.3 MB | 1.432 s | 1.039 s (−27%) |
| 300 × 300 appends | grows to ~5 KB | 0.009 s | 0.127 s (**14x slower**) |

Native builds the 1.3 MB case in 0.013 s — 110x faster than the self-host's
base, which is the gap #9077 is about.

## Why the win only appears at 1.3 MB

The self-host heap has no size class with slack in it. The small tier is an
EXACT fit — `class = words = (bytes + 7) >> 3`, which is also what every free
site recomputes to find its freelist — so a string that grows by one byte
changes class and can never be grown where it lies. Only the large tier
(>= 512 KiB, `cap = round_up(size, 512 KiB)`) has room, so an in-place arm is
unreachable until the accumulator passes half a megabyte. That is the whole of
the 27%, and it is why the 5 KB case sees no fast path at all.

Native's capacity function is where its speed comes from: 16-byte exact fit
below 2048 bytes, then THREE SIGNIFICANT BITS above it. A long accumulator
grows in place until it has used 12-25% more than it had, then copies once
into a block that much larger — amortised O(1) per byte, with no capacity word
in the header. `emitSizeClassCap` in `internal/codegen/x86_64/x86_64.go` is the
function to port; the self-host's large tier already has the shape the port
needs (round the request up to the class capacity, bump AT it, re-derive the
class from the size on free), so extending it geometrically to the small tier
follows a path the heap has already proved.

## Why the short case got slower

The lowering replaced `emit_str_concat_reclaim` with a bare
`op_call_direct("__fern_str_append", 2)`. That function is what releases a
fresh RHS temp, so `s = s + piece` stopped freeing `piece` — 90 000 leaked
strings in the second benchmark, and the allocator pressure is the 14x.

Native does not have this problem because the append is integrated with
ownership rather than dropped into the same slot: its helper CONSUMES its left
operand (the slow path is `__fern_strcat` then `__fern_str_dec(a)`), and the
IR only emits the call where the assignment was about to overwrite and reclaim
that slot, suppressing the dec-on-overwrite. A self-host port needs that
suppression, not just the helper.

## The trap: two call ABIs, one `call_direct`

`emit_ir_op_call_direct` REVERSES the on-stack arg slots, because the IR pushes
args left-to-right (arg0 deepest) and the stack ABI wants `param[i]` at
`+16 + i*8(%rbp)`. `emit_ir_op_call_direct_helper` does NOT reverse — it leaves
them as pushed. Registering a name in `ircore.is_fern_helper` routes its calls
to the non-reversing emitter, so a two-argument helper receives its operands in
the OPPOSITE order from a Fern-compiled callee such as `__fern_str_concat`.

Nothing reports this. It cost a full diagnostic cycle here, and it survived a
benchmark that appended an IDENTICAL chunk every round — because when the
accumulator is a repetition of that chunk, `chunk + s` and `s + chunk` are the
same string, and a checksum over the result cannot tell the two apart. A
benchmark for anything order-sensitive has to vary the piece.

## Next lead

Geometric size classes in the self-host heap, as their own change: one capacity
function shared by `__fern_alloc` and every free site, since a block freed to a
class it was not allocated at is the #8402 corruption. That sharing is the
point, not an implementation detail — #8404 reduced native's class arithmetic
to one definition per backend for alloc, free and the append together, and the
self-host recomputes `(bytes + 7) >> 3` inline at ~14 free sites on x86-64 and
~20 on arm64, every one of which has to agree. The append port, with
the dec suppression above, comes after it — on the shared Fern runtime
(`asmcore.rt_src_*`) rather than per-backend asm, so arm64 and wasm are served
by the same body.
