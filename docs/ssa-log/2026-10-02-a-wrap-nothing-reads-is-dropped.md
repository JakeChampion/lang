# 2026-10-02 — a 32-bit wrap nothing reads is dropped

`ssa.drop_low_wraps`, run by both register backends through
`ssa.register_form`. Refs #8171.

## The shape

The typed lowering puts a signed wrap (`int_cast:i32`, rendered `movslq` on
x86-64 and `sxtw` on arm64) after every 32-bit add, subtract and multiply, so
the register always holds the value sign-extended. Most of those wraps feed
another 32-bit add or multiply, which reads only the low half. The byte loop of
`util.hash_bucket` paid three per byte:

```
imulq $31, %r10, %r10
movslq %r10d, %r10      # read only by the add below
...
addq %rbx, %r10
movslq %edi, %rdi       # read by the comparison after the loop
addq $1, %r8
movslq %r8d, %r8        # read by the loop test
```

## What changed

A backward pass marks each value whose high half something reads: a
comparison, a division, a right shift, a call, a store, a branch or a return.
A sum, difference, product, bitwise op or left shift reads only its operands'
low halves, so it passes the mark to them only when its own result is marked;
a phi and a copy do the same. A shift's count and the narrowing unaries never
pass it on. A signed wrap whose result ends unmarked is replaced by its operand.
The pass returns before allocating anything for a function with no signed wrap,
and rebuilds only the blocks it changes. wasm keeps every wrap, since there the
wrap changes the value's type.

The three call sites that spelled the register pipeline out now share
`ssa.register_form`.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. "Stage 2"
is the compiler the self-host compiler builds from each source tree; both are
built from main at 29a15546 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 27.822 G | 27.763 G (−0.21%) |
| the five `*_bucket` hashes, self Ir | 1.447 G | 1.298 G |
| `ssa.drop_low_wraps`, inclusive Ir | — | 101 M |
| `movslq` in the stage-2 compiler's text | 11,466 | 10,646 |

The first version built a lookup table of instruction copies and rebuilt every
block of every function: it cost 202 M and the change measured 27.903 G, slower
than main. Indexing definitions instead of copying them, and rebuilding only
the blocks that change, brought it to 27.790 G; the no-wrap early return to
27.763 G.

Emitted bytes change: 656 of the 1,965 rows of the `selfhost-emit-hashes` sweep
differ from main, with the same 257 refused.

## Witnessed

`TestSelfHostOptimisationShapes` (with `wrap_dropped_before_low_reader`, which
fails on main's output on both targets), `TestSelfHostI32OverflowIR`,
`TestSelfHostRedundantWrapFlowsThrough`, `TestSelfHostSubwordWrapIR`,
`TestSelfHostU32Wrap*`, `TestSelfHostMapI32*`, `TestSelfHostSSA*`,
`TestSelfHostSemantic*`, `TestSelfHostX86*`, `TestSelfHostRc*`,
`TestFernFixturesSelfHostX86_64` (`FERN_SELFHOST_FIXTURES=1`, 557 passing, no
skips), the stage-2 build and its compile of `checker.fern`, the lint ratchet
and `make fmt-check`. The arm64 and wasm lanes, the fixpoint and the
differential suites are CI's.

## Next

Most wraps stay because the value is read whole: 93% of the `movslq` remain.
The commonest is the induction variable, `i = i + 1` under `i < n`, which
cannot overflow, so its wrap is the identity; the dominator tree in
`ssadeps.dominates` is what proving that needs. The same hash loop also
reloads the string's data pointer every byte and, on x86-64, copies the hash
into a scratch register before the multiply instead of using the three-operand
`imulq $k, src, dst`.
