# The lift's call model, and #7803 closed (2026-08-30)

With the two-word value model in place, calls were the only op class
where `internal/ssa`'s lift still disagreed with
`internal/ir/verifystack.go` about the operand stack. They are now
modelled too, and the differential reads **100% on all three ABIs**:

| config | agree |
| --- | --- |
| x86-64 one-word | 157898 / 157898 |
| arm64 two-word | 152668 / 152668 |
| wasm32 two-word | 157818 / 157818 |

Every function the verifier models, the lift agrees with at **every
reachable op**.

## Why calls needed more than the op

A call's stack effect is not derivable from the op alone:

- **Arguments.** The lift popped `op.I32`, the IR's *argument* count.
  That undercounts as soon as one argument is two-word.
- **Results.** A callee may leave nothing, one word, or a pair, and
  which one depends on its return type — which lives in the Program,
  not at the call site. `ir.ProvidedCallee` answers only for runtime
  and provided callees, so every **defined** void callee was pushing a
  phantom result. That was the whole of the 359 one-word divergences.

`ir.CallShapes` answers both. `LiftFromIRWith` takes one; the three
production callers (`buildArm64SSA`, `buildWasmSSA`, `x86_64ssa`) build
it from the program they already hold.

## One definition, not two

The rules live in `internal/ir/callshapes.go` and **`stackChecker` uses
them too** — `callArgSlots` and the result push both delegate. A second
copy would agree with the verifier by accident rather than by
construction, which is the exact drift #7803 was.

`typeSlotsABI` is the shared slot rule underneath, and
`ir.Program.TwoWordStr` records the ABI the program was lowered at, so
nothing has to ask `ast.UseTwoWordStrings` — the global the lowering
sets and restores.

## What the breakdown found that an aggregate would not

Two op classes surfaced only *after* an earlier one was fixed, each
announced by name because the gate reports which op a function first
diverges at:

- **`drop`.** Dropping a two-word value discards the pair; the lift
  dropped one word. Invisible until calls stopped diverging first.
- **`call_indirect`.** Its arguments need the same slot count as a
  direct call's, from the call site's own signature.

An aggregate percentage would have shown each fix as a partial
improvement and given no clue where to look next. The gate is now set
to exact agreement — a single divergence, of any kind, fails.

## The state of #7803

Closed. The lift's stack model is no longer an independent guess at
what the IR means; it is checked, per op, against the model the IR's own
verifier uses, over the whole conformance corpus, under every ABI.
