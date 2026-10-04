# 2026-10-03 — the plan verifier reuses the units of the function it planned

`ssaunits.verify_analyzed` and the register names of the x86-64 and arm64
emitters. Refs #8171. No emitted byte changes: the stage0-built compiler
before and after emits the fixed older tree (`examples/self_host/fern.fern`
at 1ae9cad, its bindings spelled `let`) and that tree's `checker.fern` byte
for byte.

## What the profile named

`ssarc.validate` was 1.0 G inclusive in the stage-2 compile of
`checker.fern`, nearly all of it `ssaunits.verify_planned`. Of its 910 M,
368 M went to `owned_values`, `units_of` and `units_analysis`, which the
planner had run moments before on the same function, modes and analysis.
The verifier compared the units it recomputed with the plan's takes, holds
and payloads: the same functions of the same inputs, so the comparison
could not fail. `verify_planned` already declines to re-run the analysis
for exactly that reason.

`asm_ir.ssa_reg` built `%r12`–`%r15`, and `asm_arm64_ir.ssa_reg` every
register name, by concatenating `i32_to_string`'s digits onto a prefix:
about 168,000 calls of each in one compile.

## What changed

- **The plan carries its modes and its unit analysis.** `verify_analyzed`
  takes `p.owned` and `p.unit_analysis` when it checks the function and
  modes the plan was made from. It still recomputes and compares the units
  for a plan checked against another function (`whole`) or under other
  modes. Every step replay — operations, edges, returns, invariants — is
  unchanged.
- **Register names are literals.**
- `same_takes`, a plain `i32[]` comparison, is `same_i32s` now that it also
  compares the modes.

## Measured

The compiler each tree builds from itself through the pinned stage0
(stage 2), emitting the fixed tree's `checker.fern` under callgrind:

| | after `2026-10-03-k` | this change |
|---|--:|--:|
| total Ir | 19.835 G | 19.382 G (−2.28%) |

## Witnessed

Both emit identities. The 86 SSA, units, plan, verifier and refusal tests
of `internal/e2eselfhost` pass, and stage 2 == stage 3.
