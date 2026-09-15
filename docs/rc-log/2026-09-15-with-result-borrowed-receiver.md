# A `.with` on a borrowed receiver stranded the copy it handed back

#9299, found while probing the `.with` refusal in the borrowed-param alias leg
(#9291). Four lines leak a whole buffer per call on x86-64, arm64 and wasm:

```fern
function probe(xs: i32[]): i32 {
    var w: i32[] = xs.with(0, 99);
    return w[0];
}
```

256 rounds of a 1024-element array, `FERN_LEAKCHECK` at compile time:
`allocs=2816 frees=2560 live_bytes=1576960`, against `2816/2816/0` after. It
scales with the array, not the call: at 64 elements the same 256 rounds strand
102 400 bytes, at 1024 elements 1.58 MB.

## Cause

Two facts the analyses each held half of.

`computeFreeEligible`'s `rhsTainted` reads a `__method_Array_set` result as an
alias of its receiver:

> `arr.with(i, v)` returns the receiver buffer (cow), aliasing Args[0] only

True only on `__fern_arr_cow_inplace`'s **rc == 1** arm. And that arm is
exactly what `computeArraySetIncs` makes unreachable for a borrowed receiver —
it incs one so the helper copies rather than mutating the caller's array.
`emitArraySet` states both halves where it skips the inline rc test: *"just
inc'd above: rc >= 2, so the helper's rc==1 arm is unreachable"*, and *"the
balance is: receiver keeps rc 1, the fresh copy is rc 1"*.

So on precisely the receivers where the inc fires, the result is a fresh rc 1
buffer the frame owns outright — and the taint said it was the caller's, so
nothing ever released it.

## Change

One predicate, `arraySetReceiverBorrowed`, asked by both analyses: a non-`own`,
non-owned-by-default parameter (a consumed ARRAY param included — it holds the
caller's buffer at rc 1 until its first rebind), or a non-consuming match-arm
binding. `computeArraySetIncs` already computed that set inline to force the
inc; `rhsTainted` now asks the same question to credit the result.

**They have to be co-extensive**, and that is the whole safety argument: the
credit is sound only where the inc fired. `TestWithOnBorrowedReceiverYieldsFreeEligibleResult`
asserts both verdicts per case so a change that moves one and not the other
fails, and its third case is the refusal — a local aliasing the borrowed param
is neither a param nor a binding, the inc is NOT forced, cow hands the SAME
buffer back, and crediting the result there would free the caller's array.

No ordering change was needed. The issue guessed this wanted `computeFreeEligible`
to consult the arraySetInc decision, which runs after it; in fact both inputs
the borrow predicate reads — `consumedParams` and `borrowedBindings` — are
populated before either (lines 333 and 339 against 347 and 360), so the
question can simply be asked directly.

## What it did NOT fix

Two leaks the probes turn up next door, both independent and both still open:

- an enum construction built inline in ARGUMENT position strands its box —
  `probe(Some(mk(8)))` leaks 48 bytes a call where `var o = Some(mk(8));
  probe(o)` is clean, with or without any `.with` in the callee. #9313. The
  corpus case here spells its Option through a local for that reason: the leak
  gate fails a new case that leaks, and baselining someone else's bug is what
  that rule exists to prevent.
- a STRUCT FIELD projection receiver (`bx.xs.with(…)`) was measured CLEAN
  (2048/2048/0) — the FieldAccess arm of `rhsTainted` already credits it as a
  counted alias, so the projection half of `computeArraySetIncs`' forced-inc
  set needs nothing here.

## The self-host was already right

`TestSelfHostRcPlanDiff`'s `arrayset-inc-borrowed-param` case now diverges on
`freeEligible` / `lastUses`, and the direction matters: measured over 200 rounds
of a 64-element array, the self-host reads `allocs=1400 frees=1400
live_bytes=0` and so does native AFTER this change, where native BEFORE it read
`1400/1200`, 80 000 live. The self-host never had this leak — it reclaims the
copy without listing the binding in `freeEligible`, which its `free_eligible_of`
port does not model for a `.with` result. The pin therefore records a table the
two compilers RENDER differently, not a gap in either.

## Gates

`internal/ir` whole package. The rc corpus and its leak gate on all three
backends, with the new `array_with_borrowed_receiver_result_reclaims` entry
gated at zero leaked bytes and asserting the value semantics the forced inc
exists for — the copy carries the new element, the source still reads its
original — so a credit granted where the inc did not fire is a wrong answer
here, not just a census move. No pinned leak baseline moved.
`TestSelfHostAllocDifferentialX86_64`, `TestSelfHostAllocCountMatrixX86_64` and
`TestSelfHostLeakMatrixX86_64` unchanged.
