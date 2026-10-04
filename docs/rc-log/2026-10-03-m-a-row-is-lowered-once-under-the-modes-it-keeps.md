# 2026-10-03 — a row is lowered once, under the modes it keeps

`semlower`'s ownership-inference pass, and `asm_ir`'s scan for the heap
helpers. Refs #8171. No emitted byte changes: the stage0-built compiler
before and after emits the fixed older tree (`examples/self_host/fern.fern`
at 1ae9cad, its bindings spelled `let`) and that tree's `checker.fern` byte
for byte.

## What the profile named

`semlower.rows_of` was 4.64 G inclusive in the stage-2 compile of
`checker.fern`. It ran twice on a module the inference moved: first every
row under the declared modes, then, after `with_inferred_modes`, again with
the rows the inference moved re-planned and re-lowered. The first lowering
of those rows was thrown away. For `checker.fern` that was about half its
rows, and the second lowering alone was 2.08 G.

The inference reads the produced graphs (`semsource.with_inferred_modes`
takes the `Built`), not the first lowering. The first lowering was there to
check that the module lowers whole before the inference ran.

`asm_ir.emit_function_via_ir_named` asked `ir.op_needs_heap` of every op of
every function, 99.8 M in `op_allocates`, after the first answer of true had
already settled the question.

## What changed

- **Plans first, then each body once.** `rows_planned` plans every row under
  the declared modes and, when the inference moves a row, under the inferred
  modes, taking back the declared plan of every row it did not move.
  `relowered` names the rows whose body differs between the two: those the
  inference moved, and those naming a callee whose grow mask it moved — the
  same predicate the reuse applied. `rows_lowered` then lowers each row once,
  under the inferred plans for those rows and the declared plans elsewhere.
- **The gate on running the inference is the plans.** The inference runs when
  the module plans whole under its declared modes. The inferred result is
  kept when it lowers whole, as before; otherwise the module is lowered under
  its declared modes. The one case that now differs is a moved row whose
  declared body would refuse while its inferred body lowers: before, that
  refusal failed the module; now the inferred lowering is kept. Both paths
  are checked whole.
- `rows_of`, `reused_or_lowered`, `inferred_pass` and `row_reusable` are
  gone. `plan_reusable` is `row_reusable` without the body-count clause, since
  nothing takes a body back now.
- **The heap scan stops at the first heap op.**

## Measured

The compiler each tree builds from itself through the pinned stage0
(stage 2), emitting the fixed tree's `checker.fern` under callgrind:

| | Ir | step |
|---|--:|--:|
| after `2026-10-03-l` (merged with main) | 19.394 G | |
| + heap scan stops | 19.276 G | −0.61% |
| + each row lowered once | 17.774 G | −7.79% |

## Witnessed

Both emit identities, and stage 2 == stage 3.
`TestSelfHostInferredReuseIsIdentical` passes: it compares five compiler
modules lowered with `FERN_SEM_REUSE=`, which plans and lowers every row under
the inferred modes, against the default. So do the ownership-inference,
strict-IR and semantic-IR tests and every refusal test of
`internal/e2eselfhost`, 43 in all.
