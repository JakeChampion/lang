# 2026-09-21 — an unannotated binding takes its call's type

`semsource.declare` reads a binding's type out of the checker's
post-declaration scope. For `var m = s.map(f)` on a generic receiver the
checker has nothing to give: the type is settled by the instance the call
resolves, which the checker does not carry, so the scope holds
`TypeUnknown { reason: "not yet checked" }` and the declaration refuses —
taking every caller with it through the prune cascade. `std/result`'s whole
combinator surface stood on the AST lowering because of it.

## The fix

A CALL names its own result, in the contract it reads to produce itself, so an
unannotated binding of one needs no type from the checker. When the checker
leaves the binding unsettled and the initializer is a call, the initializer is
produced with no expectation and the value's type is the binding's. It is
produced once either way; this arm just reads the answer off the value instead
of asking for it first.

The existing `closure_slot_type` arm is the same idea one shape over — a
closure binding typed from its body's contract — and stays ahead of this one,
since a `__mkclo$` call names a closure type the contract spells directly.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`, native x86-64 as the oracle.

| program | before | after | typed held |
|---|---|---|---|
| `Option.map` and `Result.map`/`map_err` through unannotated bindings | 0 of 61 | 61 of 61, 5 instances | 0 B |
| `examples/tests/result_combinators_test` | 0 of 195 | 195 of 195, 22 instances | — |

Both answer what native answers, on all four targets.

Corpus census (865 seeds), both legs run here with the same script:

| | before | after |
|---|---|---|
| programs produced whole | 819 | 820 |
| declarations produced | 83,189 of 87,203 | 83,399 of 87,218 |

No program regresses, and none produces less.

The refusal family itself is gone: `unresolved type of binding` went from 14
sites across 5 programs to none. Only one of those programs becomes whole,
because the other four stop on a different leaf — three map-iteration
conformance cases now reach `unsupported call target: Map[K, V].iter`, and
`option_combinators_test` reaches the literal below. A refusal family closing
and a program producing are not the same measurement, and the census counts the
second.

## What is still refused

`examples/tests/option_combinators_test` stops one function short, at
`s.and(Some(9))`. `Option.and` is declared `and[U](other: Option[U])`, so `U`
is bound BY the argument rather than by the receiver: the parameter type is
still a variable when the literal is produced, and a variant literal with no
concrete type to be is refused. Typing a literal from its own payload, rather
than from a destination that does not exist yet, is a different fix from this
one and is not attempted here.

## Traps

- **The whole-compiler lane is the gate that matters for this boundary.** The
  previous slice in this area passed every semantic suite, the census and the
  leak pins while producing a compiler that could not compile itself. This one
  was run against `TestSelfHostSemanticWholeCompilerX86_64` locally, before the
  push, rather than after CI said so.
