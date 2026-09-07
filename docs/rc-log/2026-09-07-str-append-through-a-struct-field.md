# 2026-09-07 — the string accumulator survives a struct field

`acc = acc + piece` grows `acc`'s buffer in place. `b = B { ...b, buf: b.buf +
piece }` — the same accumulator, kept in a struct — copied the whole buffer
every time, and there is no third way to write it: E048 forbids field
assignment, so the record-update literal IS the append. Linear code, quadratic
work (#8785).

Measured before, x86-64 `-O`, 8-byte pieces, N appends into a struct field:
**1.345 s / 8.316 s / 46.172 s** at 100k / 200k / 400k. After: **0.004 s /
0.008 s / 0.015 s** — 2.0x per doubling, which is the shape being linear.

## What was actually refusing

Not the append. The whole SITE: `structUpdateReusePlaceable` requires every
REPLACED field to be `reusePlaceableField`, and a string is not one — its slot
fans into two words on wasm and arm64 and its retain / release is per-ABI. So
`tryStructReuseOverwrite` declined and the literal lowered through the general
StructLit path: a fresh box per update, and an `OpStrConcat` that allocates a
buffer the size of everything accumulated so far and copies into it.

The box allocation was never the cost. The copy was.

## The gate is the BOX's, and it is not the string's

The reuse path is where the gate this needs already lives — its runtime
`is_unique` test — and reaching for the wrong one here is a silent
use-after-free rather than a slow program.

A struct aliased twice has box rc 2 and buffer rc **1**: only the box holds a
reference to the field, so the string looks uniquely owned to anything that
asks it. Gate the in-place append on the string and `__fern_str_append` grows a
buffer the second alias still reads — because the non-unique arm of the reuse
protocol allocates a FRESH box and leaves the old one pointing at the same
memory. The alias then observes the mutation.

So the append is the one field evaluated AFTER the uniqueness gate:

- **reuse arm** — the box is p's and nothing else names it, so the field's own
  reference is this frame's to spend. `__fern_str_append` takes it and grows in
  place when that reference is also the buffer's only one and the grown length
  still classes to the same block; otherwise it copies and releases the old
  buffer itself. Either way it OWNS what the field held, so the old-field
  deep-drop must SKIP an appended field — symmetric with `assign()`'s
  `strAppended` arm skipping the dec-on-overwrite for the bare-local form.
- **decline arm** — plain `OpStrConcat` into a fresh buffer for the fresh box,
  leaving p's field exactly as it was.

Deferring the concat does not weaken the emitter's read-after-overwrite
invariant. That one is about ordering against STORES to the box (`x: p.x + 1`,
a field swap), and no store has happened yet. The RHS is still evaluated in
step 1 in its own position, so user-visible evaluation order is unchanged.

`internal/e2e/rc_str_field_append_test.go` is the test that separates the fix
from the tempting wrong one, and it took two goes to make discriminating —
mutating the fix to gate on the string left it green twice. Both reasons are
now written into it: the accumulator must be a HEAP string, because
`__fern_str_append` refuses to grow a `.rodata` literal whatever the rc says,
and the growth must stay inside one allocator class, or the copy path is taken
for a reason unrelated to the gate. With both, the mutated compiler fails the
x86-64 and wasm legs (the arm64 leg is the deliberate control — no helper
there, so nothing to get wrong).

## What it does NOT reach: `BufWriter`

`io_buffered.fern`'s `write_string` is exactly this shape and gains **nothing**
— 2.03 s before, 2.07 s after, over 2M writes. The site is admitted now; the
frame is not.

`computeReturnSpreadReuse` needs `frameOwnsIdent(b)`, and `write_string`'s
receiver is `paramVerdictBorrowed`: borrow inference proved it non-escaping, so
the CALLER owns the box and reclaims it, and a callee that repurposed it would
be writing into live memory. That is #8785's parameter half, not its field
half, and it is the whole of what stands between BufWriter and a linear write.

The size of the prize, measured on the same shape with the receiver declared
`own` (which makes `isOwnedRcParam` true and the frame the owner): 200k
appends through the method go **8.684 s → 0.007 s**. Nothing in the tree is
written that way, and changing a public receiver to `own` changes its callers'
obligations under E050/E051, so it is a note here rather than a patch.

## Scope

One field shape, and deliberately: the value is exactly `<base>.<field> + rhs`
on the field being replaced. A general replaced string (`buf: other`) still
refuses — it needs a temp-side retain AND a per-ABI release of the displaced
value, neither of which the append needs, and it is O(1) today, so it is not
what makes an accumulator quadratic. A cross-field read (`buf: b.other + s`)
refuses for a sharper reason: the helper would consume a reference `other`
still holds.

arm64 has no `__fern_str_append` helper, so `strAppendAvailable()` keeps it on
the plain concat there and its codegen is byte-identical. Widening that
predicate without the helper is the one change its own comment calls out as
turning a release into a use-after-free.
