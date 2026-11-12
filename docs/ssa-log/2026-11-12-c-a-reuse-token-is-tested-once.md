# 2026-11-12 — a reuse token is tested once

x86-64 and arm64. `ssa.split_tokens`, run by `ssaprepare.with_selects` before
`ssa.select_branches`.

## The shape

A rebuild of a matched value tests that the value is unique. The result is a
reuse token: the box when it is unique, null when it is shared. The physical
lowering then asks the token again for each thing it does:

```
  b83:  v294 = call __fern_rc_is_unique v263
        brif v294 b100 b101
  b102: v296 = phi v263 v295          the token
        ...
        brif (v296 == 0) b103 b105    retain a child when shared
  b105: brif (v296 == 0) b106 b107    allocate when shared
  b108: v311 = phi v305 v296
        brif (v296 != 0) b109 b110    release a field, or the whole box
  b111: ...
```

Each test is a compare, a branch and a jump at the join. `ins` of the
red-black tree workloads ran three or four of them for each level of the
tree. The uniqueness test had already decided each one.

## What changed

`split_tokens` finds the token phi behind a branch: one arm is empty, the
other arm holds one zero. It follows the blocks that end in a test of the
token against zero. Each such block has two arms of one block each, or one
arm and an edge to the join.

Each side of the uniqueness test gets one new block. The block holds the
instructions of each block that tests the token. It also holds the
instructions of the arm that side takes. Each result is fresh.

The unique side reads the box where the old code
read the token. The shared side reads a zero. The decided compares and the
arms a side does not take are gone.

The block after the last join takes the two new blocks as its predecessors.
Its phis take the value of each side. A value that both sides define and a
later block reads gets a phi there.

The pass runs after the certifier. It copies instructions and removes
decided branches. It adds and removes no reference-count operation on a
path.

`bump` of the gate rebuilds a node with one field changed. Its unique path
on x86-64 is now:

```
    cmpl $1, -8(%rbx)
    jne .Lssa_bump_18
    movq 16(%rbx), %rsi
    addq $1, %rsi
    movslq %esi, %rsi
    movq %rsi, %r12
    movq %rbx, %r13
    jmp .Lssa_bump_16
```

## Measured

x86-64 container, native, Callgrind `Ir` of `bench/perceus/fern/run_*.fern`.
The base is main at `8f3b7f1b`.

| Workload | Size | Before | After |
| --- | ---: | ---: | ---: |
| rbtree | 100,000 | 245,352,360 | 215,086,116 (-12.3%) |
| rbtree-ck | 100,000 | 243,722,263 | 216,163,173 (-11.3%) |
| cfold | 14 | 10,082,615 | 9,020,910 (-10.5%) |
| deriv | 7 | 16,651,696 | 16,410,527 (-1.4%) |
| nqueens | 10 | 28,024,470 | 27,776,402 (-0.9%) |

Each answer and each allocation census is the same. Each of the five
executables is about 100 bytes smaller.

The compiler is 27,639,408 bytes, down from 27,647,920. A stage-2 compiler
runs 2,476,191,225 instructions to compile `coreutils/tr.fern`, down from
2,517,754,935 (-1.65%). Stage 2 and stage 3 of the compiler are
byte-identical.

## Gate

`TestSelfHostSSATokenSplit` reads the assembly of `bump` on both targets. It
must hold one uniqueness test and no test of a register against null.

## Trap

A call between two token tests is copied to both sides. `mirror` of
`TestSelfHostSemanticTrmc` keeps one self-call, and the assembly now has two
copies of it. The count of call sites in that test is 3, not 2.

## Next lead

The release of the `Color` field of a reused node is still seven
instructions for each level. `Color` has no payloads, so each of its values
is a static shape with no count. The fix belongs in the semantic layer: an
enum with no payloads can be a scalar tag. `ssasem.reference` takes a type
alone and can not see the enum table. The certifier refuses code that drops
only the release.
