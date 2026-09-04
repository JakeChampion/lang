# The drop a scope exit emits for a local that was never assigned

*2026-08-31* — native `internal/ir`; every backend, since all three run the
same cleanup fixpoint.

## The shape

Lowering emits an `is_unique`-gated drop for every droppable local at a scope
exit, whether or not the slot was assigned on the path that reaches it. On the
paths where it was not, the slot still holds the zero its prologue wrote,
`is_unique(null)` is 0 by every backend helper's null guard, and the entire
body the guard gates is unreachable.

That body is not one instruction. Traced in `asmcore__check_stmt`, slot 4's
drop runs from op 75 to op 506 — a tag load, a per-variant chain of payload
drops, and the box free. The guards for slots 4, 5 and 6 sit at ops 76, 507 and
938: consecutive slots, evenly spaced, a sweep over the whole frame.

## Half of it was already handled

`ConstPropagate` inlines the zero-init constant into the load, Fold's
`OpConstI32 0 ; OpRcIsUnique → OpConstI32 0` rule collapses the guard, and
`pruneConstIf` deletes the block. That chain has been in place since the fold
rule landed, and it takes the population from 5,416 to 2,680.

What it cannot reach is the branchy case. `ConstPropagate` is incremental and
says so in its own header: the slot table clears at a loop entry and after a
straight `OpBr`, because reachable execution only resumes at the merge point.

## The argument that does reach it

The IR is structured, so outside a loop every branch is forward and no write
later in op order can execute before an earlier read. A slot whose only earlier
writes are zero-init therefore holds 0 at the guard however the control flow
between them is shaped. `PruneZeroSlotGuards` makes exactly that argument, for
exactly the `OpLoadLocal ; OpRcIsUnique` shape, and hands the result to the two
folds that already exist.

Four conditions, each with its own test, and each verified to fail its test when
removed:

- the slot is a LOCAL, not a parameter — a parameter arrives with its argument,
  never a zero, and eliding its drop leaks it;
- every write before the guard is a zero-init;
- an `OpTeeLocal` counts as a write;
- the guard is not inside a loop.

The pair is replaced rather than the load alone: the load pushes a pointer and
the guard an i32, so rewriting only the load would leave the wrong type on the
stack.

## Measured over the self-host compiler

After the x86-64 backend's full battery — `Defunctionalise`, `ElideClosurePair`,
`InlineZeroCaptureClosures`, `Inline`, `FuseTee`, `EliminateDeadCode`,
`FlattenBranches`, `OptimizeCleanup`:

| | guards | total IR ops |
|---|---|---|
| before | 12,109 | 4,434,364 |
| after | **9,429** | **3,951,484** |

2,680 guards, and **10.9% of all IR ops**.

## The instrument trap this sat behind

The population was first measured after `LowerWith` and nothing else, which gave
5,416 — a number about IR that no backend ever sees, since every one of them
runs the battery above first. It overstated the shipped population by about 2x.

The general form, because this cost several passes to notice: **a measurement
over `internal/ir` says nothing about emitted code unless the backend's pass
battery has run first.** The same applies to a lift into `internal/ssa`, which
`LiftProgram` deliberately performs without `Optimize`.
