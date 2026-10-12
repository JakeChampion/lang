# 2026-10-12 — a fold looks at constants, and a schema root is walked once

`ir.fold_const_binaries`, `ir.reduce_strength_ex`, `semsource.complete`.
Refs #8171. No emitted byte changes.

## The shape

`fold_const_binaries` asked of every op whether it was a readable 32-bit and
a readable 64-bit constant before deciding it had nothing to fold. That is two
calls, `const_i32_readable` and `const_i64_readable`, per op per round, and
only the three constant kinds (`K_CONST_I32`, `K_CONST_I32_TEXT`,
`K_CONST_I64`) can answer yes. On the whole compiler's self-compile those
calls ran 25 M times. `reduce_strength_ex` asked the 32-bit question of
every op too.

`semsource.complete` hands the schema closure every value's type as a root,
so a function's few record and union types arrive once per value that has
them. `schema_of` ran 922 K times, about 85 per function, and each call looked
its record up by name and compared the types.

## What changed

- Both IR passes test an op's kind first. Only a constant reaches the
  readable checks, and nothing else they do changes.
- `complete` keeps each root once, in the order first met (`Roots`,
  `with_distinct_root`). A spelling indexes the roots, and a type equal to the
  root already spelled the same is skipped. A type without a spelling is kept
  every time, and two types that share a spelling but are not equal are both
  kept. The schema tables come out in the same order. `with_schema_root`,
  which kept every root, is gone.

## Measured

The whole compiler under callgrind. All three compilers were built from the
same tree and compiled the same frozen copy of it, each on a warm memo of its
own.

| | Ir |
|---|--:|
| before | 69.876 G |
| constants tested by kind first | 69.348 G (−0.76%) |
| and each schema root once | 69.217 G (−0.94%) |

`fold_const_binaries` −217 M, `const_i32_readable` −198 M,
`const_i64_readable` −89 M, `reduce_strength_ex` −25 M. Roots once:
`ssasem` −79 M, `semsource` −46 M, `schema_of` −13 M, against
`with_distinct_root` +28 M and `typeinfo.spelling` +15 M.

The changed compiler compiles `checker.fern` and `fern.fern` to the same bytes
for x86-64 and arm64, and `checker.fern` for wasm. It also rebuilds itself
byte for byte.

## Measured and not kept: no stack-IR optimisation on the native targets

`ir.optimize_ops` is 6.9% of the compile, and on x86-64 and arm64 the SSA
pipeline runs after it. A compiler with `optimize_ops` returning its input
compiled itself 2.3% faster, and its own text came out the same size. But the
benchmarks lost 1–4% of their speed and `tokenize` 29.6%. The SSA passes do
not fold a constant through a cast or a mask (`97 as u8` became
`movl $97 ; andq $255 ; cmpq` in place of `cmpq $97`). Keeping only
`hoist_loop_invariants` lost the same. The folding belongs in the
target-agnostic IR, where wasm, which emits the stack IR directly, gets it
too. So the passes stay, and this entry makes them cheaper instead.
