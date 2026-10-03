# A phi's constant operand loads on its edge

`ssa.imm_operands` treated a constant as an immediate only when every use
was a binary op that could read it as one. A constant a phi merges, such as
the `false` an `||` produces on the short path, or one a block returns, got a
register home like any value. Its interval then ran from its definition,
usually early in the function, to the edge or the return that reads it. When
a call sat between the two in block order, the allocator also gave it a
callee-saved register, which the function then saved and restored on every
entry. In `semtypes.is_stream`, the `false` held `%r12` across the
`__fern_str_eq` call it is never live at.

A phi operand and a returned value now keep the constant an immediate, so it
has no home and no interval:

- `ssa_phi_moves` on both register backends loads an immediate operand into
  the phi's home after the edge's copies, once nothing still reads that home.
  x86-64 stores it straight to a frame slot; arm64 goes through `x5`.
- A returned immediate is loaded straight into the result register.
- `phi_mates` skips an operand with no home, so a phi takes its register
  hint from an operand that has one.

wasm does not use `imm_operands` and is unchanged.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container, stage 2
built from main at 4577db14 and from this change on it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 26.601 G | 26.312 G (−1.09%) |
| callee-saved pushes in the stage-2 compiler's text | 89,481 | 89,065 |
| stage-2 compiler, bytes | 12,069,504 | 11,990,360 (−0.66%) |

Emitted bytes change: 998 of the 1,965 rows of the `selfhost-emit-hashes`
sweep differ from main (498 x86-64, 500 arm64, no wasm), with the same 252
refused.

## Tried first and dropped

Replacing the allocator's interval test for "lives across a call" with
liveness (`ssalive.compute`, then a backward walk flagging what is live after
each call) freed the same registers and more: the emitted code got 134 M
cheaper. But the liveness fixpoint costs blocks × value-words per pass, about
830 M over the compile; capped at 256-value functions it still cost 98 M
against an 86 M gain. Most of what it found was constants feeding phis, which
this change handles with no analysis at all. Recorded on #8171.

## Witnessed

`TestSelfHostOptimisationShapes`, including `phi_constant_loads_on_edge`,
which fails on main on both targets: it forbids the second callee-saved save
in `is_stream` and wants the returned `true` loaded straight into the result
register. The self-host SSA, x86-64, arm64, semantic, rc, i32 and alloc
suites, `TestFernFixturesSelfHostX86_64`, the stage-2 build and its compile
of `checker.fern`, the lint ratchet and `make fmt-check`.
