# A struct rebind freed a shared box's fields — the rc gate `__field_reclaim` was missing

`var p: P = P { xs: [7, 8], n: i }; ps = ps.append(p); p = P { … };` then reading
`ps[0].xs` returned garbage. Self-host 96, native 60. The two fresh arrays
declared after the rebind had reused the buffer the rebind freed.

## What went wrong

`__field_reclaim_<T>(new, old, snap)` frees the OLD box's replaced field buffers
and then the box. Its per-field guards compare `old.field[i]` against
`new.field[i]` (the cow guard — a carried-over field the new value still owns)
and against `snap.field[i]` (the caller's original, for a snapshot param).

Neither guard says whether a SECOND OWNER holds the old box. When one does,
freeing its field buffers is a use-after-free, not a leak.

Nothing reached that combination until the arrstruct live-element slice
(2026-08-25-arrstruct-live-element.md) made an appended source keep its reclaim
credit: from then on `ps = ps.append(p); p = P { … }` routed the rebind through
`emit_field_reclaim_store` while the container held the same box at rc 2. The
slice's own leak-count rows all passed — allocs and frees moved in the right
direction and the underflow guard stayed at 0 — because a use-after-READ is
invisible to both. It took a probe that checks the VALUE to see it.

## The fix

The whole reclaim is gated on `__fern_rc_is_unique(old)`, the gate every other
release in this family already applies. A shared old box takes the box-only
path, which for a LOCAL rebind is this slot handing back its counted reference
(`__fern_rc_dec`, cow-guarded so a self-store keeps it) so the surviving owner
reaches rc 1 and does the deep work. A snapshot PARAM has no counted reference
to give up — the caller owns the box — so it takes neither.

One gate at the IR level rather than three in the backends: the helper bodies
(`emit_ir_field_reclaim_one` and its arm64 / wasm siblings) are unchanged, and a
sole-owned old box — every shape that worked before — reaches them exactly as it
did.

## What it also closed

The gate is what makes the shared owner's count get handed back at all, so the
rebound-source shapes went from leaking to clean:

| shape | before the slice | slice only | with the gate | native |
|---|---|---|---|---|
| source rebound after the push | 600/200 | 600/500 | **600/600** | 600/600 |
| `append_rebind` probe | 600/200 | 600/500 | **600/600** | 600/600 |
| the value probe above | 60 (leaking) | **96 (UAF)** | **60** | 60 |

Every other row of the live-element suite is unchanged.

## The lesson worth keeping

Leak counts and the underflow counter both agreed the slice was correct. They
cannot see a read of freed-but-not-yet-reused memory, and `FERN_SANITIZE=1`
reported only the leak. What found it was asking for the VALUE with enough
allocation churn after the free to guarantee reuse. Any slice that newly admits
a release on a box a container also holds needs one row of that shape, not just
an alloc/free count.
