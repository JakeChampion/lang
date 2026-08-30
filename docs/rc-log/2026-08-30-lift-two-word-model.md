# The lift's two-word value model (2026-08-30)

`internal/ssa`'s lift walked the flat IR assuming **one operand-stack
entry per value**. That is true only under the one-word string ABI. On
arm64 and wasm a string is a `(data, length)` pair, so the lift ran a
word short at every string and the SSA it produced described a different
program from the one the IR did.

`TestLiftAgreesWithTheVerifiersStackModel` compares the lift's height
after each op against `internal/ir/verifystack.go`'s, per op, over the
conformance corpus. It reports which op kind a function FIRST diverges
at, and that breakdown — not the aggregate — is what made this
tractable: each op class is a separate, checkable claim.

## What the model now says

| op | two-word behaviour |
| --- | --- |
| `const.str` | pushes `OpConstString` **and** `OpConstStringLen` |
| local slots | a two-word slot gets two consecutive entries in `l.slots` (`planSlots`) |
| parameters | a two-word parameter becomes two SSA parameters, both recorded in `ParamIRIndex` |
| `local.load/store/tee` | move the slot's entry count |
| `load`/`store` with `WidthString` | fan out to `addr+0` and `addr+PtrW` |
| `str.eq`/`str.cmp`/`str.concat` | each string operand is a pair; concat returns one via `AddCallPair` |
| `str.len` | no call at all — the length **is** the second word |

Result: every non-call divergence class is gone from both two-word
columns. What is left is calls, on all three targets.

## The trap this area sets, again

`ir.TypeIsTwoWord` asked `ast.UseTwoWordStrings`, which reads a **global
the lowering sets and restores**. #7824 fixed `stackChecker.twoWordStr`
but the same global still reached `typeSlots` and `localSlots` by that
second route, so the arm64 column was a hybrid: an honest call model
over a one-word slot model. It read 77.29%.

That is why arm64 "fell" to 70.53% here. Nothing regressed — the two
figures were never measuring the same thing. `TypeIsTwoWordABI` takes
the ABI as an argument; the global-reading wrapper stays for callers
that run *during* lowering, where it is correct.

The general rule: **anything holding an `ir.Func` after lowering must
read `f.TwoWordStr`, never `ast.UseTwoWordStrings`.**

## Why the aggregate stopped being the gate

Two earlier attempts at this work failed by iterating against the
coverage percentage, which oscillated (99.96 → 94.10 → 90.15 → 92.52 →
89.99). An aggregate moves for reasons unrelated to the change —
corpus growth, a lowering change, a fix to the verifier itself — so it
cannot tell a step forward from a step sideways.

The test now asserts the **breakdown**: only call-shaped ops may be a
function's first divergence, and any other class fails outright. A
correct step shows up as an op class disappearing, which no percentage
shuffle can imitate. The floor stays, demoted to a collapse detector.

## What calls still need

Both halves need a program-level callee table the lift is not given:

- **Arguments.** The lift pops `op.I32` — the IR's *argument* count —
  which undercounts the moment one argument is two-word. The verifier
  reads `ArgTypes` through `typeSlots`.
- **Results.** `ir.ProvidedCallee` answers for runtime and provided
  callees, which is how the void-call push was found and fixed. A
  **defined** callee has no such entry, so the lift cannot tell whether
  one returns nothing, one word, or a pair. That is the whole of the
  359 remaining one-word divergences.

`LiftFromIR` takes a single `*ir.Func`; supplying the table is an API
change, and the next slice.
