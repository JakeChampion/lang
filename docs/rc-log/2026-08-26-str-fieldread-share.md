# The string field READ is a counted share

Closes `str__fieldread` on the construction-retain matrix — 400 allocs / 200 frees
against native's 300/300. Matrix: 7 leaking cells → 6.

```fern
var q: P = P { f: mkv(i), n: i };
var p: P = P { f: q.f, n: i };      // the read: leaked
```

The read lowers through `struct_get` to the source box's buffer, so the new box
co-owns it. The share was uncounted, and the credit pass had marked BOTH holders
box-only — each correctly, while the read is uncounted: `q` because a field read
in a struct-literal field is a positive MOVE position, `p` because its literal
borrows a string it did not retain. `slot_nodeep` then withholds
`__struct_drop_P` from both, so neither holder frees the string at all: two
leaked boxes per round, exactly the 200-free shortfall.

## Hoisting the same read was already clean

The finding that made this tractable is a minimal pair — the same program, the
same answer, differing only in where the read lands:

```fern
var tmp: string = q.f;              // clean: 400/400
var p: P = P { f: tmp, n: i };
```

`strfld_safe_operand` forgives a direct field-read init (the #4768 read-side
retain counts it), so the hoisted spelling routes and sweeps normally. Only the
inline spelling leaked. That pair is what turned a vague "the fieldread cell
leaks" into a single question with a two-line reproduction.

## Three readings of the code were wrong before a dump settled it

Worth recording, because each was plausible and each cost a build:

1. **The `NODEEP:` / `FLDCHECKED:` markers.** Correct, but abandoned early —
   `__field_reclaim_P` was missing from the emitted asm too, which read as "the
   type is not routed at all" and sent the search upstream. That helper is only
   emitted when some slot needs the consume-rebind path, and with both slots
   `NODEEP` none does. The missing helper was a CONSEQUENCE of the marker, not a
   second cause.
2. **`strfld_collect_unsafe` descending into the struct literal** and marking the
   field name. It has no `ExprStructLit` arm at all — the arms are
   FieldAccess / Binary / Unary / Index / Slice / Call — so it cannot mark from
   there.
3. **STRFLDOK admission**, which the matrix file's own comment blamed
   ("retain gated on strfldok admission, not granted here"). A temporary dump at
   the return of `strfld_reclaim_ok_types_of` showed `unsafe_names` EMPTY and `P`
   ADMITTED in both spellings. Admission is identical between the leaking and the
   clean form; it is not the cause, and that comment is misleading.

What settled it was dumping `reclaimable_names_of`'s rows for the pair:

```
inline:   NODEEP:p@6:5      NODEEP:q@5:5
hoisted:  FLDCHECKED:p@7:5  FLDCHECKED:q@5:5
```

Two instrumented builds cost about eight minutes and replaced three wrong
readings. On this surface, dump the verdict rather than re-read the predicate
that produces it.

## One predicate, three sites

`str_field_share_read` decides the retain in the `ExprStructLit` lowering and the
two marker flips in `bind_var_slot`. The retain was already firing for this shape
via the ownership test (a field read is Borrowed, never Owned or Static), so the
fix is marker-side in effect — but the retain asks the predicate anyway, so the
retained set and the flipped set are identical by construction rather than by two
conditions agreeing (#7253). A marker flipped without the inc behind it turns one
box under two rc-aware `k_str` decs into a free followed by a dangle.

Kept as a separate predicate from `enum_arr_field_share_read` rather than
widening that one: it bottoms out in array-ness tests a `type A | B` union
passes, which would put `parser.Stmt[]` — and so every statement block in the
compiler — inside the class.

Flipping the verdict means WRITING `FLDCHECKED:`, not merely revoking `NODEEP:`.
They are two arms of one either/or and a block-scoped slot deep-drops only on the
second (#6127); `blockscoped` is the row that catches a revoke-only change.

Both holders' types must route field reclaim. A source whose type does not route
would keep leaking while the target released a box it now co-owns.

## Measured

Self-host x86-64 against the native oracle:

| probe | before | after | native |
|---|---|---|---|
| `local` (control) | 300/300 clean, e73 | unchanged | 200/200 clean, e73 |
| `fieldread` | **400/200, live 5600**, e74 | **400/400, live 0**, e74 | 300/300 clean, e74 |
| `hoisted` (control) | 400/400 clean, e74 | unchanged | 300/300 clean, e74 |
| `value probe` | 1000/800, live 5600, e45 | **1000/1000, live 0**, e45 | 600/600 clean, e45 |

The value probe is what carries the soundness. It puts three fresh allocations
between the share and the read-back, so a box freed too early is recycled and the
length sum changes. Exit 45 is identical across before, after, and native — and
that is the only thing separating a correct fix from an over-release, because
`allocs == frees` is what BOTH look like. `__rc_underflow_count()` and
`FERN_SANITIZE=1` are clean on every shape; the sanitizer reports no
use-after-free, double free, or poison anywhere.

## What stays refused

Both deliberately keep the leak they had, and both are load-bearing:

- **`respread`** — a `T { ...base }` copies every field pointer into a fresh box
  with NO inc, minting an uncounted third owner. Gated by FIELD TYPE
  (`LowerState.spread_sites`), not by holder name, because the dangerous base can
  name a local with no slot yet when the share is decided. 500/300 before and
  after.
- **`moved_ret`** — no bind, so no marker flip, and the inc goes with the move
  (#6726). 400/200 before and after.

The new test asserts these do NOT balance: if either starts balancing, the gate
that refuses it has stopped firing.

## What is left

`str_arr__fieldread` does NOT move with this and still leaks — the `string[]`
flavour of the same RewriteCtx shape. It is the obvious next limb, but it needs
its own measurement rather than an assumption that this generalises: the
`string[]` element walk is rc-gated differently (`__fern_str_arr_free` decs and
leaves the elements to the other owner at rc > 1), so the counted-share protocol
may already hold there for a different reason.

Tests: `internal/e2eselfhost/self_host_str_fieldread_share_test.go`, 8 cases
across x86-64 / arm64 / wasm. Every `want` was measured against the native
x86-64 backend, never read off the self-host run under test.
