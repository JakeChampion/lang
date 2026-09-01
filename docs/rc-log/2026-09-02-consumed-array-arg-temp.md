# The temp nobody owned: a fresh array at a consumed-threaded parameter

Found by instrumenting the argument-temp admission gate itself rather than
by chasing a survivor. Printing every call position where
`freshOwnedRcTempType` says "this is a fresh owned temp" but
`reclaimArgTemps || reclaimIndirectArgTemps || countedArgTemp` declines it
yields **60 distinct sites in the whole self-host compiler** — the reclaim
already covers nearly everything — and the accumulator-fold family is most
of what remains:

```
5  astwalk__collect_idents_expr#1  string[]
5  astwalk__collect_bound_stmts#1  string[]
4  irlower__collect_returns#1      ast__Expr[]
5  irlower__box_scan_stmts#6       irlower__BoxScan
```

## Two correct analyses, one uncovered value

`fold_all([], items)` leaks its 16 B array literal on every call, and both
halves of the reason are individually right.

The callee's parameter is **consumed-threaded**, so it runs the
ownership-flag protocol (`emitConsumedArrayOverwriteDec`). That flag starts
at 0, meaning *this slot still holds the caller's borrow* — so the first
rebind deliberately does not release the incoming buffer, and neither does
anything later. Correct: it is a borrow.

The caller's stage-(b) reclaim asks `paramCountedRetain`, which reads false
for a parameter the body hands out bare (`return out`). Also correct, for
the question it was asked.

Nobody asked the question that matters: the callee's protocol *proves* it
never freed the argument, so a fresh temp passed there has no owner at all.
`consumedArrayParamPositions` answers it per callee, and the admission gate
reads it.

Arrays make that a projection of `computeConsumedParams` rather than a
duplicate of it: `isOwnedByDefaultType` has no `ArrayType` arm, so for an
array parameter `paramOwnedByDefault` is always false, `paramVerdict` is
always `NotOwnedType` and `typeIsStringArrayFree` is always false — all
three verdict-dependent gates are dead, leaving reassigned + not-`own` +
`consumedDropWired`. `TestConsumedArrayPositionsMatchTheLoweringVerdict`
pins the agreement, and earned its place on the first run by catching an
`own` parameter where the two legitimately differ.

## The guard, and why it is not optional

The callee can hand the temp straight back: nothing rebinds `out` when the
loop body never runs, so `fold_all([], [])` returns the very buffer the
caller is about to release, at the one reference it holds. The drop
therefore sits behind a pointer-changed test against the call's result —
the same shape the array overwrite and `isSelfMapMutation` use. A result
that cannot be a pointer cannot be the temp and takes no test.

## Measured

x86-64 and arm64 alike, 8 rounds, `__rc_underflow_count()` folded into
every exit, `-interp` the oracle:

| probe | before | after |
| --- | --- | --- |
| `fold_all([], items)` | 128 | **0** |
| the same plus `fold_all([], empty)` | 256 | 128 |

The residual 128 is the hand-back half: the guard declines, and the
result's own reference keeps `rhsTainted`'s conservative call-result taint.
That is a leak the guard is choosing over a double free, and it is pinned
in both gates as `consumed_array_arg_temp_released_and_guarded`.

**Driver: 313,792 → 312,128 B (−1,664, +104 frees)**, output
byte-identical. Smaller than the census suggested: the ranked row that
pointed here (`irlower__assign_target_into`, 119 blocks / 7,584 B) counts
the accumulator's grown *generations*, while the temp actually leaked per
call is the 16 B literal — 104 of them.
