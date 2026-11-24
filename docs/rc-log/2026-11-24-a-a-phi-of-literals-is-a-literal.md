# A phi of literals is a literal

2026-11-24. Each target. The change is in `rccertify`.

## The gap

The certifier reads a string or an aggregate literal as an immortal box. A
phi input that is a literal hands on no unit and no debt (`free`). But
`literal` looked only at the definition of the value. A phi of two literals
has no slot of its own, so the certifier did not track it. A phi input from
it handed 0 units.

`ssa_lift.lift_impl` has this shape. It ends a block on several arms, and
each arm sets `cur_insts = []`. The lowering joins the literals of two arms
in a phi. That phi then meets the pushes of `cur_insts` on the other arms.
The meet of 0 units and 1 unit is maybe. The maybe then goes round the loop
of the function into each value that the loop carries.

## The change

A phi whose inputs are all literals is a literal (`literal_within`). In a
loop, an input can be the phi itself. The search goes 4 phis deep at most.
A deeper phi is not a literal.

The cases `literal-phi-of-literals-returned` and
`literal-phi-of-literals-leaked` of `TestSelfHostRcCertifyCases` check the
shape. Without the change, the first case has 1 unproved value.

## Measured

`scripts/certify-census` on the same tree, without and with the change:

| row | before | after |
|---|---:|---:|
| `x86_64/fern_main.unproved` | 1,058 | 934 |
| `wasm32/checker.unproved` | 135 | 123 |
| `x86_64/checker.refuted` | 5,449 | 5,449 |
| `x86_64/checker.weakened` | 230 | 230 |
| `x86_64/checker.missed` | 218 | 218 |

`lift_impl` goes from 128 unproved values to 52. The mutant rows do not move.

## Next lead

Of the 52 values left in `lift_impl`, the first is `stack`, an `i32[]` that
the loop carries. At its joins, one arm keeps the array and another pushes
onto it. `phi_input` makes a value a twin of itself only when the value is a
parameter, and `stack` is a loop phi. Examine that rule first.
