# 2026-10-21 — const_propagate's known table starts on one placeholder

`ir.const_propagate`. Refs #8171.

## The shape

The pass tracks one constant op per slot. A load of the slot becomes
that op, and `has[]` says which slots hold one. The table started on a
fresh `op_const_i32(0)` per slot. That is one nine-field `Op` box per
slot of every function the pass ran over, 1.1 M in the whole-compiler
self-compile. No read saw a placeholder: the pass reads a slot only
after `has[]` marks it, and the read gets the op that marked it.

## What changed

Every slot starts on the same placeholder op. The reads, the writes and
the output of the pass are unchanged.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is one frozen copy of main at 2210347.

| subject | main at 53442b3 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 73.741 G | 73.650 G (−0.12%) |

The whole-compiler binary is byte-identical to the base compiler's. When
the compiler compiles `compiler/checker.fern`, it performs 0.5% fewer
allocations: 26,156,335 against main's 26,298,117 at ddc5b11 on the dev
container. The allocation lane's baseline re-banks that figure.

## What is left

The box shim's other callers of any size are the records their passes
produce. `ssa_lift.lift_from_ir_prod` builds its instructions (3.6 M).
`ssaunits.plan_analyzed` builds its steps (1.4 M), each through a chain
of record copies that the reuse analysis turns in place.
`ssarc.instruction_body` builds the emitted ops (1.2 M). The checker
builds its annotations (0.9 M) and the parser its type references
(1.3 M).

The allocation count itself, 81 M boxes and 93 M frees per self-compile,
is now the churn of the compiler's own data structures. No single site
answers it.
