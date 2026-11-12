# 2026-11-12 — a call behind a return join is a tail call

x86-64 and arm64. `ssa.return_calls`, run by `ssaprepare.with_selects` after
`ssa.select_branches`.

## The shape

`return a != b && rest(x)` lifts to a join. The call block and each
short-circuit block jump to one block, which holds a phi and returns it.
The emitters make a tail call only when a call ends a block and the block
returns the result. The call behind the join was thus an ordinary call:
a frame, a `call`, a jump to the join and a return for each level.

`safe` in `bench/perceus/nqueens.fern` has this shape and is 67% of the
instructions of the n-queens workload.

## What changed

`return_calls` finds each block that holds one phi and returns it. Some
predecessors end in a call, jump to that block and give the phi the result.
Each such predecessor now returns the result itself. The result must have no
other use.

A predecessor that is a join of call results takes the return in
the same way. The search goes at most four joins deep. The join keeps its
other predecessors. A join with one predecessor left returns the value of
that predecessor. A join with no predecessor goes.

The pass moves no instruction, so the proof of the certifier holds. The
blocks that take a return also take the source position of the join.

`safe` is now a loop with no frame on both targets:

```
.Lssa_safe_3:
    addq $1, %rsi
    movslq %esi, %rsi
    movq %r9, %rdi
    jmp __fn_nqueens__safe.r
```

## Measured

x86-64 container, native, compiler at `8bc115d8` against the same tree with
the pass. Instructions are Callgrind `Ir` of `bench/perceus/fern/run_*.fern`.

| Workload | Size | Before | After |
| --- | ---: | ---: | ---: |
| nqueens | 10 | 32,025,418 | 28,212,142 (-11.9%) |
| rbtree | 100,000 | 271,064,146 | 271,064,146 |
| rbtree-ck | 100,000 | 268,995,237 | 268,995,237 |
| deriv | 7 | 17,010,722 | 17,010,722 |
| cfold | 14 | 10,660,407 | 10,660,407 |

Wall time of n-queens at 13, three runs each: 1207, 1306 and 1340 ms before,
1075, 989 and 999 ms after. The recursion depth no longer grows with the
length of the list.

The compiler itself grows from 27,720,960 to 27,743,200 bytes (+0.08%).
Each block that takes a return carries an epilogue. Stage 2 and
stage 3 of the compiler are byte-identical.

## Gate

`TestSelfHostSSATailCallThroughJoin` reads the assembly of an `&&` chain, an
`||` and a nested `if` expression on both targets. Each must jump to the
register entry of its callee, build no frame and make no call.
