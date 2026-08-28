# The ELB walk looks through a value-block IIFE

The lead both of today's earlier entries left open:

```fern
function rd(src: E[], i: i32): i32 {
    return (match (src[0]) { E.A(xs) => xs.len(), E.B => 0 });
}
```

The statement form of this — `match (src[0]) { … }` with the arms returning —
has been admitted since the extract-then-die widening. The EXPRESSION form was
refused, and the caller's element walk went with it: **4 allocs / 2 frees,
80 bytes live** where the statement twin is 4/4/0.

## Why it was refused

A match-/if-EXPRESSION desugars to a zero-arg call of a zero-param lambda.
`arrenum_esc_expr`'s lambda arm says, correctly for a real lambda:

> A lambda capturing the name carries it out of the frame entirely.

For this shape the premise is false. `parser.is_value_block` marks the desugar,
and irlower **inlines** those statements rather than calling them — the read
happens in this frame and dies with the statement. The walk was refusing on a
capture that does not outlive anything.

## The change

`arrenum_param_escapes` — the "ELB:" flag walk only — now looks through a value
block at the three positions where it meets a whole expression: a `return`, an
expression statement, and a `var` initialiser. Looking through means running the
SAME walk over the inlined statements, so every arm proof the statement form
gets, this gets too: `arrenum_param_arm_ok` still confines each binding, and a
handout still refuses.

The look-through is deliberately NOT in `arrenum_esc_expr`, which the local
class shares — the same reason the scalar-payload entry gives for its parallel
predicate. This is the flag question only.

## Measured (x86-64, 100 rounds, `E { A(i32[]), B }`)

| shape | before | after |
|---|---|---|
| `return (match (src[0]) { … })` — the lead | 4/2, 80 live | **4/4, 0** |
| statement `match (src[0])` (control) | 4/4, 0 | unchanged |
| element handed out THROUGH the block (`H { e: src[0] }`) | refused | refused, exit 6 = interp |
| the whole element returned from the block | refused | refused, exit 6 = interp |

Both handout rows leak by design and compute the exact value with the underflow
detector at zero — the assertion there is the exit, not the byte count, because
a wrong admission is a use-after-free rather than a leak.

## What is still refused, and it is the residual lead

The block must be the WHOLE value of its statement. Nested inside arithmetic —
`return (match (src[0]) { … }) + i` — the walk meets the call through
`arrenum_esc_expr` and still refuses. Covering that means a parallel expression
walk for the flag question rather than a look-through at three statement
positions, which is a wider change than this lead names; it is left open rather
than half-claimed. The suite row pins the bare form deliberately.

## A trap this cost me

The first row I wrote was the nested-in-arithmetic form, copied from the
neighbouring `callee_extracts_element` row, which passes for a different reason
(it binds `var e = src[0]` first and rides the local-confinement proof). It
failed at exactly the unfixed number, which reads like the fix not working. It
was the row asking for a shape the change does not claim. **Check what the
neighbouring row's admission actually rests on before reusing its shape.**

## Gates

`TestSelfHostArrEnumBorrowedArgX86_64` with the two new rows — the admit row
fails on the parent at 4/2, 80 live, and the handout row passes in both states;
the arrenum reclaim / counted-param / field-share / producer suites and the
arrstruct siblings that share the flag builder; the construction matrices; and
`TestSelfHostStage2FixpointArm64` (97 s), the gate this change class needs.
