# Phase B's positive half, and why it was missing (2026-08-31)

`2026-08-30-result-axis.md` ended on the fixpoint's share of the gap:
`ownership_returns.go` proves `ReturnBorrowed` and has no way to say
`ReturnOwned`. This is that half.

## One answer was three answers

`classifyValue`'s `clsOwned` meant *"freshly allocated, read out of
memory, or produced by a callee whose return is owned. Also the answer
for anything not understood"* — its own doc comment listed the union.

That was fine while the pass had exactly one verdict to reach. "Is every
returned value a borrow?" only needs to know whether something blocks,
and all three block. The moment you want the OTHER verdict the merge is
fatal: a function that plainly returns what it allocated is
indistinguishable from one the pass has never heard of.

Split into `clsFresh` (provably carries a unit) and `clsUnknown` (not
understood). Both still block a borrow proof; only `clsFresh` supports
an owned one.

## What it measured, over the self-host compiler

| | before | after |
| --- | --- | --- |
| address-returning functions | 3873 | 3873 |
| proved to return an OWNED unit | **0** | **1494** |
| proved to return a borrow | 84 | **127** |
| consumed parameters (phase A) | 368 | 368 |

The borrow half improving was not the point of the change and is the
more interesting number. It moved because a call to a runtime helper
whose RESULT axis says immortal — a `.rodata`-headered `Option` box —
now classifies neutral rather than blocking. That is
`2026-08-30-result-axis.md` feeding this pass, one slice later.

Phase A is untouched, which is the invariant worth stating: 368 before
and after, and the two-model agreement stays at 95.39%.

## The rules that needed writing down

**A mix is not a verdict.** One return borrowing and another allocating
gets nothing, not "owned because one of them was". `mergeClassifications`
makes unknown dominate and a borrow-versus-fresh disagreement collapse to
unknown.

**A neutral value yields.** `Some(box)` on one arm and the `None`
sentinel on the other is OWNED: a release of a static sentinel
short-circuits on its rc word, so the caller may release the result
whichever arm ran. The same reasoning that makes `__str_slice` owned
even when it packs a string inline.

**A load is not fresh.** `return self.field` hands back a reference the
container owns; calling it owned would tell every caller to release
something it never acquired. It stays unknown, which is where the 2252
remaining functions mostly sit.

**The verdicts latch.** A round can only turn "unproven" into an answer,
because the extra information a later round carries is a callee
signature that was unknown before. That is what keeps the alternation
with phase A terminating; rounds went 4 to 5.

## A null result worth recording

The ownership family read `sigs[o.Str]` while `units.go`, `certify.go`
and `width.go` read `sigs[ir.CodegenAlias(o.Str)]` — and `width.go`'s
own doc says why the alias matters: *"a Map call site names `map_new`
while the module holds `map_new_impl`"*, which was a link failure in
#6609. Made consistent.

**It changed nothing measurable**: opaque call sites stayed at
4889/687292, consumed at 368, owned at 1494. Recorded because the next
person to notice the inconsistency should not have to re-measure it —
the incoherence was real, the defect it could have caused was not
present on this program.

## Next

`make_closure` is the one class the certifier still reports against the
oracle — 102 findings, and genuinely open in both directions since
closure reclamation is on `docs/TEST-GATES.md`'s live gap list.

The 2252 functions with no return verdict are dominated by loads. Giving
them an answer needs a notion of "reachable from" that is weaker than
`ReturnBorrowedFrom`'s "is another name for", which the anchor mechanism
deliberately does not have.
