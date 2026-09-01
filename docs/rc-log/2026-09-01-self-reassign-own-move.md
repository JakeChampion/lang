# The re-move that rebinds: `p = f(…, p, …)`

*2026-09-01* — native `internal/ir`; surfaced while porting rel8 branch
relaxation to the self-host x86 assembler (#7949), which pushed compiling
`lexer.fern` from 4.85 MB of append-cliff traffic to 415 MB.

## The shape

```
function outer(own p: P, k: i32): P {
    p = inner(p, 10);        // a transfer — retained
    return inner(p, 10);     // the last occurrence — claimed
}
```

Move-on-call claims an `own` param at its textually-last occurrence only, and
`computeReturnOwnMoves` (#6125) extends that to the other *return* sites. A
transfer that is neither — an assignment whose target is the param itself —
kept paying `ownArgNeedsRetain`'s compensating retain.

That retain was never balanced. A self-reassign emits no overwrite-dec:
`callConsumesIdent` suppresses it precisely because the callee owns and drops
the old binding. So the extra reference had nothing to spend it — a leaked box
per call, and the callee growing `p`'s buffers at rc>1, so its first append
copied the whole thing.

## Why the claim is sound

The site *rebinds* `p`. The reference the callee consumed is gone from this
frame, and every later mention of `p` — the exit sweep included — reads the
fresh result the call returned. Two such sites in a row are each sound for the
same reason: each consumes the binding current at that point, so no liveness
analysis is needed. The guards mirror `computeReturnOwnMoves`: the assignment
target must be the param itself (`q = f(p)` would leave `p` dangling for the
sweep), `p` must occur exactly once in the right-hand side, and the occurrence
must be a bare ident at an `own` position of a direct call.

## Attribution notes (what actually worked)

The regression looked like the relaxation's own doing, and three plausible
theories died before the real one:

- The struct-update `a = X86Asm { ...a, br_long: … }` at the top of a function
  that does not own `a`. Real enough to fix (the decisions moved into a
  constructor, `x86_asm_new_for`), but the traffic did not move by one byte.
- Seven new `X86Asm` fields. Adding all seven to the pre-relaxation file, with
  no code change at all, left the cliff at exactly 12,794 / 4,853,888.
- The rel8 emission path. Forcing every branch long — byte-identical output to
  the base — still cost 145 MB.

What settled it was bisecting the *code* rather than the behaviour: from the
pre-relaxation file, adding one call — `x86_branch_record` after
`x86_fixup_or_patch` in `x86_jcc_label` — reproduced the whole regression
(4.85 MB → 115 MB) with no relaxation anywhere. Swapping the two calls changed
nothing, which named the shape: a chain of two `own` calls on the same param.

Pinned by `rc_own_remove_test.go` case `F_chained_transfers` (49 crossings over
50 calls before, 0 after; all three backends). With the fix the relaxed
assembler compiles `lexer.fern` at 13,035 / 5.09 MB — a 4.8% premium on the
base for the extra relaxation passes — and `checker.fern` no longer exhausts
the arena.
