# 2026-10-02 — the fold pass skips the ops no arm reads

`ir.fold_const_binaries`. Refs #8171. No emitted byte changes: the
`selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0
differing against a compiler built from main at 9cc02c45, and the
`checker.fern` binaries the two stage-2 compilers emit are
byte-identical.

## What the profile named

`fold_const_binaries` was 496 M self of the 35.38 G stage-2 compile of
`checker.fern`, 97% of it in the 206 instructions its inner loop runs
on every one of its 2.34 M iterations (a `--dump-instr` profile). The
loop has nine arms, every one gated on the op at hand being a readable
i32 or i64 constant, or a string constant for the `str_eq` arm, and an
op that is none of those still paid each arm's gate in turn: a load of
`folded`, the `i + k < cur.len()` bound, the constant flag, and the
register shuffles the allocator put between the arms. Most ops are not
constants.

## What changed

One test after the two readable checks: an op that is not an i32, i64
or string constant goes straight to the copy. The arms, their order and
their gates are untouched, so every fold happens where it did.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 9cc02c45 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 35.38 G | 35.03 G (−0.99%) |
| stage 2, `fold_const_binaries` self Ir | 496 M | 147 M |
| stage 2, `fold_const_binaries` inclusive Ir | 563 M | 215 M |
| stage 2, `propagate_fold` inclusive Ir | 1.10 G | 0.91 G |
| stage 2, `optimize_ops` inclusive Ir | 2.36 G | 2.01 G |

The remaining 147 M is 63 instructions per iteration: the two readable
checks (a call each, 81 M between them inclusive), the copy into `out`
once a pass has folded, and the loop itself.

## Witnessed

`TestSelfHostConstExprFold*`, `TestSelfHostConstfold*`,
`TestSelfHostGenericASTFold*`, `TestSelfHostIRStrengthPeephole`,
`TestSelfHostModloadFoldsTargetOS`, `TestSelfHostSemanticSourceRC`, the
lint ratchet, `make fmt-check`, and the emit-hash sweep.

## Next

`optimize_ops` is still 2.01 G: `propagate_fold` 0.91 G, of which
`const_propagate` and `propagate_copies` are now the larger part, and
the strength fixpoint re-walks the whole list per round. The pass
restarts its walk from the top after every fold (`while (changed)`),
so a function with k folds is walked k + 1 times; a walk that resumes
at the fold would take the 2.34 M iterations down toward the 1.41 M
ops. Elsewhere, self cost: `ssa_lift.lift_impl` 1.10 G, `__fern_alloc`
1.04 G, `util.hash_bucket` 0.81 G (519 M of it from
`NameIndex.chain`, 2.6 M hashes at about 200 instructions each).
