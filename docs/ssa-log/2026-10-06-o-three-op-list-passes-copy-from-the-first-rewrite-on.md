# 2026-10-06 — three op-list passes copy from the first rewrite on

`ir.prune_zero_slot_guards`, `ir.fuse_tee` and `ir.reduce_strength_ex`,
three passes of the op-list battery `ir.optimize_ops` runs on every
lowered function. Refs #8171. No emitted byte changes: the compiler before
and after builds `checker.fern` for x86-64, arm64 and wasm, and
`fern.fern` for x86-64, byte for byte.

## What changed

All three rebuilt the whole op list on every run, appending each op they
kept to a fresh list, and most runs keep every op: a function with no
uniqueness guard on a still-zero slot, no store followed by a load of
the same slot, or no multiply by a power of two got its list copied for
nothing, one `Op` record (and its string's retain and release) per
element. The strength pass runs to a fixpoint, so its last round was
always such a copy. They now scan until the first rewrite, copy the
prefix there (`fold_prefixed`, the shape the dead-tee pass already had)
and append from then on; a run that rewrites nothing returns its input.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at a0c4de43 by the stage0 pin.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 16.951 G | 16.837 G (−0.67%) |
| `ir.reduce_strength_ex`, self | 92.0 M | 46.3 M |
| `ir.optimize_ops` (the guard and tee passes inline into it), self | 201.5 M | 168.1 M |
| the `Op` release, self | 52.6 M | 34.5 M |

The guard and tee passes are 42 M of that and the strength pass 72 M.

## What is left

The fold, the dead-code sweep and the copy propagation already copy from
the first rewrite on. What the battery still pays per function is its
rounds: `propagate_fold` and the strength fixpoint each run their passes
once more than they rewrite, and that last round reads the whole list to
find nothing.
