# A `replace` result is releasable only behind an identity guard

`var r: string = base.replace(old, new)` leaked its box whenever the needle was
present. 400 rounds of the churn harness, a pair of compilers from the same
commit:

| shape | x86-64 | wasm |
| --- | --- | --- |
| needle present | 54400 → **0** | 48000 → **0** |
| needle absent | 0 → 0 | 0 → 0 |

## Why this is not the trim treatment

`2026-08-20-trim-view-box-release.md` argued that `str_local_binding_is_fresh`'s
excluded-forms comment mislabelled `trim`, and it did. It does **not** mislabel
`replace`. Measured directly, one call each, bytes allocated:

| | x86-64 | wasm |
| --- | --- | --- |
| needle ABSENT | **0** | **0** |
| needle PRESENT | 136 | 120 |

Zero means the receiver's own box comes back — a true identity. Releasing it
frees a box the receiver still owns, and the receiver's own release then
double-frees. Reading the comment and stopping there would have been wrong for
trim; taking trim's answer and applying it here would have been wrong too. The
measurement is what separates them, and it is two probes.

## Fix

The analysis cannot settle which case it is — the needle is a run-time value — so
the credit says "this box MAY be ours" and a guard settles it where the answer
exists. `emit_str_slot_release` frees under `result != receiver`, the same cow
test `emit_str_reclaim_store` already uses, pointed at the receiver slot that
`LocalInfo.str_replace_src` records at the binding site.

Only a bare-ident receiver qualifies. The guard needs something the frame still
holds to compare against, and a temporary receiver could not be the identity
anyway, since nothing else names it.

Comparing against a slot the sweep may already have freed is safe: the guard
compares pointers and never dereferences, and either order gives the right answer
— identical means skip, different means this box is ours.

## Witnessed at fault level

A compiler with the credit kept and the guard removed over-releases on the
liveness case: **exit 99 (rc underflow) on x86-64 and a trap on wasm**, against a
clean main and a clean fix. That probe holds both shapes at once — `same` is
base's own box, `diff` is fresh, both credited, only one may be freed.

This is the guard's own witness, distinct from the release's: the release is
witnessed by the heap gate (98 on the parent), and the two cannot substitute for
each other. Note which direction each fails in — a missing release is a leak the
byte gate sees, a missing guard is an over-release the byte gate would score as
an IMPROVEMENT.

## Shape of the family, after four increments

`join_strarr_init`, `tostr_scalar_init`, `trim_str_init` and now
`replace_str_init` all read the same declared-name/type pair to settle the
receiver's type, because the syntactic classifier they feed is state-free by
design. Three of the four then credit unconditionally; this one needs the extra
run-time test. That is the axis worth remembering: *what the receiver is* is a
compile-time question, *whether the result is the receiver* need not be.
