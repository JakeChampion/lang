# 2026-10-21 — two records leave the leaf inliner and the reachability walk

`ircore.captured_leaf_word` and `semreach`'s walk. Refs #8171.

## The shape

The whole-compiler profile puts the box shim at 81 M calls. 60 M of them
are the compiler's own struct constructions. Two of its callers build a
record once per step and drop it at once.

`captured_leaf_word` compared each op's tag with the tag of a freshly
constructed `ir.op_call_word()`. That is one nine-field `Op` box per op
of every function the leaf inliner walks: 2.9 M in the self-compile, and
184 M Ir with the construction and the drop.

The reachability walk returned a `Stepped { wk, changed }` record from
every step: one allocation per block entered, edge taken or value
moved, 2.7 M from `entered` alone, 227 M Ir of self time there.

## What changed

`K_RET_WORD` and `K_CALL_WORD` name the two tags, as the other tags are
named. The inliner reads them. The constructors keep their literal tags,
which the kind-registry test pins.

`Walk` carries `changed`. Each round clears it at its start, and the
step that moves something sets it. So the steps return the Walk itself,
and the `Stepped` record is gone. A step that changes the Walk already
copies the record, so the flag rides that copy. The rounds, their order
and what they compute are unchanged.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is one frozen copy of main at 2210347.

| subject | main at 53442b3 | tags | tags and Walk |
|---|--:|--:|--:|
| `compiler/fern.fern`, the whole compiler | 73.741 G | 73.422 G (−0.43%) | 73.224 G (−0.70%) |

The whole-compiler binary is byte-identical to the base compiler's after
each commit. When the compiler compiles `compiler/checker.fern`, it
performs 4.5% fewer allocations: 25,362,276 against main's 26,564,102
on the dev container. The allocation lane's baseline re-banks the
runner's own figure, 25,361,399. That is 877 under the dev container's
count for the same sources and subject. The five-count path spread that
the baseline file describes does not cover that gap, and nobody chased
it.

## What is left

The box shim's other callers of the same shape, by their call counts in
the profile: `ssa_lift.lift_from_ir_prod` 3.6 M, `ssaunits.plan_analyzed`
1.4 M, `ssarc.instruction_body` 1.2 M, `ir.const_propagate` 1.1 M and
`checker.annotate_typed` 0.9 M. `const_propagate` builds a constant op
per propagated load. Read each before it becomes a slice: some are the
records the pass is for.
