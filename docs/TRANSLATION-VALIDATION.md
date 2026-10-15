# Translation validation

Status: first slice (#5543). One pass is validated: strength reduction
(`reduce_strength_ex` in `compiler/ir.fern`).

The e2e corpus, the fixpoint and the differential oracles find a miscompile
only when some test runs the input that triggers it. Translation validation
proves each rewrite the compiler makes equivalent to the code it replaced, for
every input, with an SMT solver, in the way LLVM's Alive2 checks InstCombine.
A counterexample is a concrete entry state on which the two versions disagree.

## How it runs

1. `FERN_TV_DUMP=strength` makes the compiler write every op list the pass
   rewrote to stderr, before and after (`tv_dump` in `compiler/ir.fern`). Each
   op is written with all of its fields, so two dumped ops are equal exactly
   when their lines are.
2. `internal/testing/tv` reads the dump back (`Parse`), pairs up the differing
   straight-line runs (`Split`), writes each pair's equivalence as an SMT-LIB
   bit-vector query (`Encode`), and runs the queries through Z3 (`Prove`).
   `unsat` is a proof; `sat` comes back with the solver's model
   (`Counterexample`).
3. `TestDifferential_TranslationValidationStrength` (`internal/testing/e2e`)
   compiles fernsmith programs with the dump on, proves every distinct run
   once, and fails on a counterexample, a solver timeout, or a run whose
   stack shape or value widths changed. It also fails when it proves nothing,
   so a dump that stops appearing cannot pass silently.

Z3 is a test dependency only, pinned in `mise.toml`. Nothing the compiler or a
compiled program does runs it.

```
FERN_TV_SEEDS=2048 go test -run '^TestDifferential_TranslationValidationStrength$' ./internal/testing/e2e/
go test ./internal/testing/tv/
```

## The model

The model covers the integer fragment of `docs/INTEGER-SEMANTICS.md` at 32 and
64 bits:

- constants (`const_i32`, a decimal `const_i32_text`, `const_i64`)
- `add` through `shr_u`, and the comparisons (`eq` through `ge_u`)
- `not` (`eqz`)
- `int_extend` and `int_wrap`
- `int_cast` to `i32`, `u32` and `u8`, and `u32_wrap`
- `load_local`, `store_local`, `tee_local` and `drop`

An op whose width is not 0 or 64 is outside it. Inside it, arithmetic wraps, a
shift count is masked to the width, `x / 0 == 0`, `x % 0 == x`, and
`MIN / -1 == MIN`. The 32-bit meaning is the one `wasm_ir.fern` emits, where an
i32 value is 32 bits wide. The native backends keep i32 values in 64-bit
registers, and the high half of those registers is not part of the IR's
meaning. Signedness is in the op kind (`div_u`, `lt_u`), so an op's `unsigned`
flag is not modelled.

Every other op is a **barrier**. `Split` cuts both op lists at their barriers
and requires the two barrier sequences to be identical. It then proves each
pair of differing runs between barriers equivalent as a state transformer.
The input state is an arbitrary entry stack (`s0` is its top) and an arbitrary
value in every local (`l<n>`). The claim is that both runs leave the same
stack and the same value in every local either run wrote. Each run is proved
for any input state, and the barriers are the same ops applied to the same
state, so the whole rewritten function is equivalent to the original.

A run's input takes its width from its first use; the source side is
well-typed, so that is the real width.

## What it does not check

- **A pass that changes a barrier.** `Split` reports this and does not
  validate it. Strength reduction never changes a barrier, so the oracle
  fails if it does. Passes that do change barriers (the fold's branch pruning,
  dead-code elimination) need control flow in the model first.
- **Facts from outside the run.** Each run is proved for every input state,
  so a rewrite that depends on what an earlier barrier established (for
  example const-propagation across a `block`) would be refuted even when it
  is sound. Strength reduction is local and does not depend on such facts.
- **Floats, pointer-width ops, memory and calls.** These ops are barriers.
- **A width introduced only on the after side.** If a pass makes the after
  side read an input at a width the before side never fixed, the model
  accepts the width the after side reads. The IR verifier
  (`FERN_IR_VERIFY`) is the gate for typing.

## Measurements

On 2026-10-15, 2048 fernsmith programs and the stdlib code they reach gave
940 distinct rewritten runs. All 940 were proved, in 97 s on a 4-core
container.

To check that the oracle detects a real bug, a planted rewrite of signed
`x / 2^n` to `x >> n` was added to `strength_rewrite`. This is the rewrite the
pass deliberately refuses, because `div_s` rounds toward zero and `shr_s`
rounds toward negative infinity. The oracle refuted it on seed 9 of 256, with
a model in which the dividend is negative and not a multiple of 8.

## CI

- `test-e2e-differential`: 256 seeds per PR, split across that lane's shards.
- `nightly-fuzz`: the whole 2048-seed window, which moves forward each night.
- `test-units`: `internal/testing/tv`'s own tests. They prove every rewrite in
  the strength table, and refute the rewrites the pass must not make.

## Widening

Widen the model only when a pass needs it. `fold_const_binaries` is the
natural next pass: its constant folding is already inside the fragment, and
its branch pruning needs `if`, `br` and `brif` modelled across runs.
