# The ELB tier admits extract-then-die element reads

The widening the borrowed-arg suites' `callee_extracts_element` rows asked for
by name: the "ELB:" flag's callee-side walk (`arrenum_escapes`, len()-only by
design) now runs through `arrenum_param_escapes` for the flag question, which
admits an element read `name[i]` in exactly three shapes:

- the exact scrutinee of a STATEMENT-form `match`, every arm binding (payloads
  and the at-binding) confined to its arm by the strict `binding_escapes_arm`
  (`.len()`, indexing, arithmetic-over-reads admit; a bare re-bind or bare
  assign refuses);
- the whole init of `var e = name[i]`, the local confined by the same proof
  pair the box flag trusts for a param name — `body_unsafe_for_match_borrow`
  plus `param_match_binding_escapes`;
- a whole-array alias `var x = name`, when the alias itself passes this same
  walk (recursion bounded by the def chain).

Every other position of `name[i]` — a struct-literal slot, an append or call
argument, a return value — stays an escape. That boundary is what keeps
`element_handed_out` refused, and it holds through the alias admission too: an
alias whose own body hands an element out fails the alias's walk. All inner
proofs run under empty registries, so the flag stays registry-independent and
the interproc fixpoint cannot oscillate. The flag builder is shared, so the
arrstruct class gets the same admissions; its suite's extract row (a field read
off the extracted local, no match) rides the local-confinement proof.

## Measured (x86-64, 100 rounds, rc-payload `E { A(i32[]), B }`)

| shape | before | after |
|---|---|---|
| `var e = src[0]` then match on e (producer keep) | 4/2, 80 live | **4/4, 0** |
| same, literal keep | 4/1, 112 live | **4/4, 0** |
| statement `match (src[0])` direct | 4/2, 80 live | **4/4, 0** |
| `var x = src` alias, statement match on `x[0]` | 4/2, 80 live | **4/4, 0** |
| match-EXPRESSION on `src[0]` (the IIFE desugar) | 4/2, 80 live | 4/2, 80 live (held — see next lead) |
| handout via struct-lit field (`element_handed_out`) | refused | refused (1400/1000, exit 25 = native, no underflow) |
| handout THROUGH the alias (`var x = src; H { e: x[0] }`) | refused | refused |
| arm binding returned (`E.A(xs) => return xs`) | refused | refused |

Sanitize leg on all four granted shapes: zero findings, exits unmoved. Both
`callee_extracts_element` rows flipped to `balance: true` pins; the arrenum row
previously pinned only the exit — the stale-row lesson its struct twin taught.

## Traps met on the way, for the next widening here

- **A match EXPRESSION is not a match statement.** `return (match (src[0])
  { … })` desugars to a 0-arg IIFE, so the walker meets an ExprLambda whose
  capture set contains the param — the lambda-capture escape, not the
  scrutinee path. Admitting it means teaching the walk the IIFE shape; left
  refused (a safe constant leak) and it is the next lead.
- **Scalar-payload enum arrays leak for a different reason entirely.** `enum
  Tag { Box(i32), Nil }` behind the same shapes stays 3/2, 40 live however the
  flag answers: no element walk is emitted for an enum whose payloads are all
  scalar (`enum_arr_elems_walk_ok`), so the element boxes strand with no
  credit to grant. A probe here reads exactly like an ELB refusal and is not
  one — check the payload kind before blaming the flag. Separate gap, not
  taken in this slice.

## Gates

Both borrowed-arg suites with the flipped pins; the counted-param and reclaim
floors of both classes; all four matrices; rc corpus both legs; and
`TestSelfHostStage2FixpointArm64` — the arrstruct suite names it as the one
instrument that caught this tier's unsound widening, so it is the gate that
makes the balance trustworthy.

## Next lead

The match-expression IIFE form above, and the scalar-payload walk-emission gap
beside it — the second likely subsumes a family (any enum array with no rc
payload strands its boxes).
