# A constant record is one static box

Part of #8920.

## What changed

The AST lowering has placed a struct literal whose every field is a scalar
literal in static data since #6149 (`op_const_struct`). The semantic lowering
did not, so every `t_i32()` in the self-host checker built a fresh
`TypeI32 { is_char: false, width: 32, unsigned: false }` box. In one compile of
`coreutils/tsort.fern`, the constant type constructors alone made about 260,000
heap allocations.

`ssaunits.plan` now records `constants`: the static words for each record or
boxed-variant construction whose every field is a narrow scalar (`i32`, `u32`,
`u8`, `char`, `boolean`) and whose value is a constant. That value is an
integer or boolean constant, or a negative `i32` literal. `0 - k` reaches the
plan as a subtraction on one path and a negation on the other, and both are
admitted. Closure environments and option-layout enums are left alone.

`ssarc.instruction` emits `op_const_struct` for those. The x86-64 and arm64
emitters and wasm already place it, record or variant, and its immortal rc makes
every release and uniqueness test pass over it. A constant construction also no
longer claims a reuse donor: `recipient_slots` answers -1 for it. Otherwise the
donor would be held for a construction that no longer takes it.

## Measured

`TestSelfHostStaticBoxes`, 100 rounds per probe, heap allocations. The results
are identical on all four targets:

| probe | before | after |
|---|---|---|
| record with `0 - 3`, `7`, `true` | 100 | 0 |
| union members `TInt { 32, true }` and `TVoid {}` | 200 | 0 |
| enum payload `Mixed(0 - 2, true)` | 100 | 0 |
| member holding a string (control) | 100 | 100 |
| record from a parameter (control) | 100 | 100 |
| a dying record beside a constant one | 100 | 100 |

The self-host compiler built by itself, compiling `coreutils/tsort.fern`
(callgrind, x86-64):

| | frame views only | with static boxes |
|---|---|---|
| instructions | 4,090,982,616 | 4,047,464,013 (−1.1%) |
| stage 2 linked bytes (`-g`) | 13,111,352 | 13,112,832 |

Stage 2 → stage 3 is byte-identical.

## Still allocating

A constant with a string, wide or float field: each needs more than the one
narrow word per field the backends place today. `t_unknown(reason)`, which makes
139,000 boxes in the same compile, is not one of these: its field is a
parameter, so the box is not constant in the function that builds it.
