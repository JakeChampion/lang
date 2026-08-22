# The fresh array a METHOD returns, released

#7259, and only one of the three defects that issue turns out to name. This one
is the unbounded one.

| rounds | 100 | 200 | 400 |
| --- | --- | --- | --- |
| `hh.mkm().len()`, self-host before | **4000** | **8000** | **16000** |
| after | 0 | 0 | 0 |
| the byte-identical FREE function | 0 | 0 | 0 |

`frees=0` throughout the before column — not "the elements strand", *nothing* is
released. Exactly 2.0× per doubling. Native and interp are flat on every row.

## The gap

`arr_fresh_ret_fns_of` gated on `funcs[k].receiver_type.len() == 0`, and the
`"ARR:"` registry entry keyed `funcs[k].name`. Free functions only.

Every consumer was **already method-aware**. `owned_fresh_call_callee` resolves a
`"<Base>.<method>"` key and has a header paragraph explaining how it does so —
the same key form the strict-fresh registry's own struct entries have always
used. The consumers were looking up a key the producer never wrote.

That is the shape worth remembering: not a missing feature, a *half-built* one.
Grepping the consumers finds method handling everywhere and reads as complete.

## Three sites had open-coded the free-function half

The registry key was one of them. The other two are call sites that re-derived
the admission instead of asking:

| site | before | now |
| --- | --- | --- |
| `mk()[i]` read reclaim (#6491) | `owned_fresh_call_callee` | unchanged — started working the moment the key existed |
| `mk().len()` receiver reclaim | inline `match` on `ExprIdent` callee | routed through `owned_fresh_call_callee` |
| discarded `mk(i);` statement | `ExprIdent` arm only | gained an `ExprFieldAccess` arm |

The `.len()` site is the instructive one: it had copied the free-function lookup
rather than calling the resolver that already existed two hundred lines away, so
it could not be fixed by fixing the registry. Deleting the copy is what fixes it.

## What is refused, and why that matters more than what is admitted

`function (h: H) get(): i32[] { return h.xs; }` hands back the receiver's **own**
buffer. It is not admitted — `body_has_nonfresh_arrlit_return` sees the field
return — and it must not be: releasing that at the call site frees a buffer the
live `hh` still owns. The refusal is pinned by its own test, asserting the answer
and `__rc_underflow()` rather than a byte count, because that shape still leaks
48 bytes from the *other* two defects and asserting 0 would pin a bug.

The admitted-but-subtle case is the mirror: `return [g.xs[0], g.xs[1]]` reads the
receiver's field and is still fresh, because the elements are copied scalars. The
registry's question is where the returned BOX came from, not where its values did.

## The method-key space cannot collide with the bare-name one

The struct walk (`fresh_struct_fwd_fixpoint`, `arr_field_ident_is_frame_built`)
is the other consumer of `arr_fresh`, and it asks bare-name questions. A method
key contains a `.`, which an identifier cannot, so those lookups cannot match a
new entry and their answers are unchanged by construction — the same
prefix-multiplexing argument the file already runs for `"ARR:"` against the bare
struct names in the same list.

## The two defects this does NOT close

#7259 reads as one bug and is three. Measured separately (`i32[]`, one call, no
loop, so the loop is not doing the work):

- **The return-transfer dup is never released.** `return h.xs` emits an
  unconditional `__fern_rc_inc` and no caller decs it. Removing the dup fixes two
  shapes — and fails #7232's own suite at **exit 99 on all three backends**, so
  the dup stays and the fix is caller-side.
- **The struct loses its deep field-drop.** Calling *any* function that returns
  one of its array fields demotes the caller's struct to a shallow
  `__fern_arr_dec` of the box; the field's buffer is stranded. Read off the
  emitted asm: `main`'s exit is one `arr_dec` of the struct box and no field walk
  anywhere. Two controls place it — a scalar-returning method on the same
  receiver is clean (not the receiver escaping), and the function *defined but
  never called* is clean (not the declaration).

Both are bounded per object, where this one was unbounded. They interact: whoever
restores the field-drop has to agree with whoever releases the dup, or together
they over-release.

## Trap

A hand-built probe of #7232's shape reported **clean** with the dup removed —
exit 34, no underflow, leak improved 104 → 64 — while #7232's own suite failed at
99 on three backends. The probe did not recreate the credit class the real case
lands in. A reconstruction of a fixed bug is not a regression test for it; the
suite that shipped with the fix is, and it was two minutes away.
