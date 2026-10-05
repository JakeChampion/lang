# 2026-10-05 — an else-if merge shares the loop header's home

Self-host SSA register allocator, x86-64 and arm64. Refs #8171.

## The shape

An else-if chain inside a loop, each arm updating one of many loop-carried
values:

```fern
while (i < ops.len()) {
  let k: i32 = ops[i];
  if (k == 0) { v0 = v0 + k; } else if (k == 1) { v1 = v1 + k; } ...
  i = i + 1;
}
```

At each merge down the chain, every value the arm left alone arrives as a phi
whose operand from before it is the loop header's phi. That operand is read
again at the loop's back edge, so its interval covers the merge, and
`phi_mates` gave the merge phi no hint it could share: it took a home of its
own. With more carried values than registers, every arm's edge then copied
each untouched value from the header's home into the merge's, through the
frame.

The two never hold different values on any path: the header phi is dead by
the time the merge phi is defined, because every path past the merge reaches
the back edge through the merge phi, not the header phi. Liveness by position
says they overlap. Liveness by path says they do not.

## The change

- `phi_mates` records, per phi, the first operand from before it that
  outlives it (`Mates.alt`).
- Placement tries alt's register first. It shares it when no value holding
  that register is live where the phi is defined, by path
  (`chain_dead_at_def`, `live_at_def` on each value in the register's
  `shared_with` chain).
- A phi whose alt is spilled and dead at the phi stays spilled, so
  `path_slot` gives it the same slot.

The allocator does more path queries. Two changes keep that cheap:

- `live_at_def` copies a shared all-false block table on its first write,
  rather than building one per query.
- `chain_dead_at_def` skips a value whose interval ends before the phi's
  begins.

## Measured

The loop above with twenty values, built for x86-64:

| | main | merge shares |
|---|--:|--:|
| `f` instructions | 489 | 257 |
| `f` moves | 290 | 54 |
| `f` frame | 184 bytes | 104 bytes |

`checker.fern` built for x86-64-linux under callgrind, main at 2657bb61
against this branch. Stage 2 is built by the fixed stage-1 compiler, so it
measures the allocator's own cost. Stage 3 is built by stage 2, so it measures
the code the allocator produces:

| | main | merge shares |
|---|--:|--:|
| stage 3, total Ir | 18.547 G | 18.456 G (−0.49%) |
| stage 3, `ssa_lift.lift_impl` | 816 M | 754 M |
| stage 2, total Ir | 18.804 G | 18.826 G (+0.12%) |
| stage 2, `live_at_def` | 57.9 M | 61.8 M |
| stage 2, `phi_mates` | 22.7 M | 23.2 M |

Before the two cost changes, stage 2 was 18.890 G and `live_at_def` 124 M.

The stage-3 compiler reproduces itself: stage 3 and stage 4 are
byte-identical for x86-64.
