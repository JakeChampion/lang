# 2026-09-21 — the poison check guards the uniqueness tests

#9889. `san_poison_check` guarded eight rc sites on each backend and not the
ninth: `__fn___fern_rc_is_unique`, nor the uniqueness test the register path
renders inline for it. Found while porting the quarantine to arm64 (#9882),
which mirrored the x86-64 emitter site for site and so inherited the gap on
both ISAs rather than introducing a divergence.

## What it cost

A stale reference that reaches a uniqueness test rather than a retain or a
release reads the poison, finds it is not 1, and answers "not unique". The
caller takes its copy path. Under `-sanitize` the run stays silent and exits
0, where the same stale reference one instruction earlier or later exits 124
with the named report.

Answering "not unique" is the safe answer — the block is not reused and
nothing is corrupted — which is why the gap was easy to miss. But a sanitizer
that has the evidence in a register and drops it is worse than one that never
looked: a reduced repro whose stale touch happens to land on a uniqueness test
comes back clean. Reuse makes that likely rather than theoretical, since FBIP
asks `__fern_rc_is_unique` before every in-place update.

## The fix

The check each backend already had, at two more sites apiece. x86-64 needs no
scratch — its check is a compare against an immediate. arm64 does, and the two
sites want different registers:

- the stub takes **w9**, which is inside the `x0/x2/x9/w3` clobber set the
  stub documents and `emit_arm64_field_reclaim_one` relies on to keep x1 (the
  field value) live across the call;
- the inlined arm takes **w7**, because x4, x5 and x6 all hold something
  across the compare — the pointer, the count, and the 0/1 answer — and x7 is
  not in the home pool (x0, x9..x15, x19..x28), so the check can borrow it.

## Why the gate is an asm contract

There is no source-level way to stage this. The checker exposes `__rc_inc`,
`__rc_dec`, `__rc_get` and `__rc_underflow_count` and no `__rc_is_unique`:
the uniqueness test is emitted by `ssarc`, in code the compiler itself proves,
so a stale one is a compiler bug rather than something a test program can
write. `uafSelfHostIncSrc` works for the retain side only because `__rc_inc`
is an intrinsic.

So `TestSelfHostSSARcPrimitivesAreInline` gained the contract instead, and it
is a counting one rather than a presence one: `Blk.put` has three inline rc
chains — two uniqueness tests and a retain — and the flag-on listing must
carry three poison checks, not merely one somewhere in the function. That is
what the old assertion missed. The flag-on listing of
`__fn___fern_rc_is_unique` is checked alongside, since a function the lift
declines still calls the stub.

Verified both ways: with the emitter change reverted the gate reports
`3 inline rc chains but 1 poison checks` and a stub that reads the count
without checking it, on x86-64 and arm64 alike.
