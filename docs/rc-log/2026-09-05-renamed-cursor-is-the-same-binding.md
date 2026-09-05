# 2026-09-05 — a renamed cursor is the same binding, in both directions

`var c: C = c0;` at the top of a state-threading function made every append
below it copy the whole array. #8498's repro emits three instructions where its
one-emit sibling emits one — 3x the work — and took **1500x** the time:

| shape | before | after |
|---|---|---|
| `inner` — one emit, returns | 1 ms | 1 ms |
| `outer` — emit, call, emit through distinct locals | 3086 ms | 3 ms |
| `outer3` — the same, rebinding one local | 2990 ms | 2 ms |
| `outer2` — the same written as nested calls, no locals | 2 ms | 2 ms |
| `outerP` — `outer` without the rename (threads the param) | 2 ms | 2 ms |
| `outerR` — `outer` over a callee that constructs on every return | 3006 ms | 2 ms |
| `outerT` — rename + distinct locals, no intervening call | 1945 ms | 1 ms |

20000 iterations, `-O`, x86-64-linux, one struct with an `It[]` field. The
issue reads the trigger as "the cursor is named a second time"; the table says
it more precisely — `outerP` and `outer2` are the two spellings without the
rename, and both were already linear. The rename is the whole trigger.

## Why it was slow

Two mechanisms both keyed on "is this name a parameter?", and a rename is not.

**The alias retained.** `computeMovedLocals` moved an alias out of an owned
LOCAL (`isOwnedRcLocal`) and not out of an owned PARAMETER, so `var c: C = c0;`
paid a transfer inc and left c0's reference alive to the exit sweep for the
whole body. The container therefore sat at rc 2 and `__fern_arr_cow_inplace`
took the copy path at every field append. `movableAliasSource` is the union of
the two roles, which is the same predicate `emitRcDecLocalsAtExitExcept`
releases on — "will this frame dec it?" now has one answer for both. A BORROWED
parameter stays out: moving it hands away a reference the caller owns.

**The death was withheld.** `callArgDeaths`' last-occurrence shape admits a
param and a local bound from a direct call; a local bound from a plain ident
was excluded by the alias rule ("another live name in this frame reads the same
buffers"). A rename taking its source's ONLY occurrence has no such name, so
`renameRoots` resolves it and `aliasInitLocal` admits it on its source's
footing. Chained renames close under the rule itself.

`movableAliasSource` alone fixes only `outer3`, and the death alone fixes
nothing — the pair is what flattens the table.

## The unsoundness the pair opened, and the third piece

Admitting the death without teaching the rest of #4873 about the rename is a
value-semantics bug, not just a missed optimisation. `computeGrowParams` asked
`paramIdx(fn, arg.Name)` when propagating a growable position outward; under a
rename the argument is a LOCAL, so the propagation stopped there, the enclosing
function reported no growth summary, and ITS caller left a live binding
unbracketed. Measured: the caller read back 23 elements where the interpreter
oracle says 22 — the exact #4873 divergence the bracket exists to prevent.

`paramIdx` resolves through `renameRoots` now. One helper answers "which
binding is this name?" for both analyses, because a split answer is precisely
what withdrew the bracket. `internal/e2e/append_borrowed_param_test.go`'s
`renamed-cursor` case is that leg on x86-64 and arm64; it reads 23 with the
resolution removed and the rest of the change in place.

## Witnessed

- `internal/ir/rename_alias_death_test.go` — the retain and the bracket, each
  with its anti-vacuity twin (source read after the rename keeps both).
- `internal/ir/call_arg_death_last_use_test.go` — `rename_chain`,
  `rename_twice`, `rename_literal` in the exact-death table. The literal case
  pins the boundary: a rename is admitted on its SOURCE's footing, and a struct
  literal is not an admitted source.
- Conformance leak census and the three rc corpus leak legs, per
  `docs/TEST-GATES.md`'s standing instruction for any change to the death
  verdicts or the containment bracket.

## Still open

The tuple-return spelling the issue records under its workaround is unmoved:
`return (C { ...c, insts: c.insts.append(in) }, in.op)` measures 916 ms before
and 940 ms after against 0 ms for the same emit returning the struct alone. It
is a different mechanism — #6665's field-place RETURN admission does not look
inside a returned tuple — and has its own issue.

The self-host's port (`grow_param_flags_of` +  `grow_sole_exempt_names_of`)
carries only the SOLE-occurrence exemption, not the last-occurrence family this
sits in, so nothing here has a self-host counterpart yet. That gap is strictly
conservative: more brackets, more copies, no divergence.
