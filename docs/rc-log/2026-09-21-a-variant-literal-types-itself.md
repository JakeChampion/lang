# 2026-09-21 — a variant literal types itself where the parameter cannot

`Option.and` is declared `and[U](other: Option[U])`. The parameter binds `U`
from the very argument being typed, so the destination a `Some(9)` literal is
produced against is `Option[U]`: the right union, with nothing settled in it.
`literal_union` preferred that destination over the checker's reading whenever
the two named the same union, `variant` found it not concrete, and the literal
was refused — taking `examples/tests/option_combinators_test` with it.

## The fix

A destination that names the union but settles nothing in it is no more use
than none at all. `variant_call` already had the answer for a `Some` with no
destination — `some_of`, which reads the payload's own type — and it was
reached only when the destination named no variant at all. It is reached now
when the destination names one but is not concrete.

The checker cannot stand in here: it infers the literal from the same
parameter, so its reading is unsettled for the same reason. The payload is the
only thing that can say what the `Some` holds.

The destination still wins where the literal's own reading is the vaguer of the
two, which is what a payloadless `None` against an annotated slot is.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`, native x86-64 as the oracle.

| program | before | after | typed held |
|---|---|---|---|
| `s.and(Some(9))` beside `s.and(other)` | 0 of 52 | 52 of 52, 2 instances | 0 B |
| `examples/tests/option_combinators_test` | 0 of 196 | 196 of 196, 22 instances | — |

Both answer what native answers, on all four targets.

Corpus census (865 seeds), both legs run here with the same script:

| | before | after |
|---|---|---|
| programs produced whole | 820 | 822 |
| declarations produced | 83,399 of 87,218 | 83,678 of 87,218 |

Two programs become whole: the option combinator suite and the
`option_result_combinators` conformance case. Nothing regresses.

## Traps

- **Two unsettled readings do not make a third.** The obvious fix is to prefer
  the checker's type when the destination is not concrete, and it does nothing
  here: the checker infers the literal from the same parameter, so both are
  unsettled together. Only the payload is independent of the binding that has
  not happened yet.
