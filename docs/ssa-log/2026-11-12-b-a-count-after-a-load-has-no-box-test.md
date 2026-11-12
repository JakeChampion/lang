# 2026-11-12 — a count after a load has no box test

x86-64 and arm64. `ssadeps.boxed_values`, `asmcore.ssa_is_boxed`, and
`ssa_rc_guard`, `ssa_rc_dec` and `ssa_unique_flags` in both register
emitters.

## The shape

An inline retain, release or uniqueness test first tests that its value is a
box. On x86-64 the test of a uniqueness test is four instructions:

```
    testb $1, %bl
    jnz .Lssa_ins_uq3
    cmpq $0x10000, %rbx
    jb .Lssa_ins_uq3
    cmpl $1, -8(%rbx)
```

A match reads the tag of its subject before it rebuilds the subject. The
uniqueness test of the rebuild thus comes after a load from the same value.
A null value stops the program at the load. The front part of the test can
not fail.

These tests were 7.4% of the instructions of the rbtree and deriv Perceus
workloads and 6.6% of cfold.

## What changed

`ssadeps.boxed_values` gives, for one function, the values whose counts need
no box test. A value is one when each `__fern_rc_inc`, `__fern_rc_dec` and
`__fern_rc_is_unique` of it comes after a load from it. The load is an
`ld_at` with the value as its base and an offset that is not negative. It is
in the same block before the count, or in a block that dominates the count.

Each emitter computes the table before it allocates registers and keeps it
in `EmitState.ssa_boxed`. `ssa_rc_guard`, `ssa_unique_flags` and the floor
test of `ssa_rc_dec` read it. A `__fern_str_free` keeps its tests. A
sanitizer build keeps each test.

The uniqueness test above is now one instruction:

```
    cmpl $1, -8(%rbx)
```

## Measured

x86-64 container, native, Callgrind `Ir` of `bench/perceus/fern/run_*.fern`.
The base is main with #15522.

| Workload | Size | Before | After |
| --- | ---: | ---: | ---: |
| rbtree | 100,000 | 257,448,370 | 245,352,342 (-4.7%) |
| rbtree-ck | 100,000 | 255,418,337 | 243,722,245 (-4.6%) |
| deriv | 7 | 17,010,722 | 16,651,678 (-2.1%) |
| nqueens | 10 | 28,212,142 | 28,024,452 (-0.7%) |
| cfold | 14 | 10,660,407 | 10,082,597 (-5.4%) |

Each answer and each allocation census is the same as before.

The compiler is 27,584,640 bytes, down from 27,745,152 (-0.58%). A stage-2
compiler runs 2,527,716,962 instructions to compile `coreutils/tr.fern`,
down from 2,543,222,212 (-0.6%). That count includes the new analysis. Stage
2 and stage 3 of the compiler are byte-identical.

## Gate

`TestSelfHostSSABoxedCountHasNoGuard` reads the assembly of two functions on
both targets. `bump` matches its parameter and rebuilds it: only the counts
of the two children keep the floor test. `twice` counts a parameter that
nothing reads: its count keeps the test. A sanitizer build of `bump` keeps
all three tests.

## Trap

The proof is for one SSA value. A copy or a phi of the value has no proof,
and its counts keep the test.
