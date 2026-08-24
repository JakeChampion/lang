# A match expression is not a closure — killer-drops slice 6

Reading a struct local's field through a `match` EXPRESSION cost that local —
and every other struct local in the same function — its reclaim credit.
Nothing was released at all, not even the holder box.

## The measurement that found it

Four probes, identical but for how the field is read (100 rounds, x86, exit
parity with native throughout):

| probe | shape | result |
|---|---|---|
| sa1 | `f: i32[]`, alias bind, `p.f.len()` | 200/200 clean |
| sa4 | `f: i32[]`, alias bind, `pick(p.f)` call arg | 200/200 clean |
| sa3 | `f: E`, alias bind, `p.n` only | 300/300 clean |
| sa2 | `f: E`, alias bind, `match (p.f) {…}` in value position | **300 allocs / 0 frees** |

sa3 against sa2 is the whole finding: same struct, same enum field, same
alias bind. The match expression is the only difference, and it takes
releases from 3/round to zero. The enum field kind — the thing this looked
like from the matrix — is not implicated at all.

## Mechanism

A match expression is not an AST expression. parser.fern desugars it to a
zero-arg IIFE marked `ORIGIN_MATCH_EXPR`, one of the VALUE BLOCK origins that
irlower INLINES rather than calls. No closure is ever built, so nothing can
outlive the expression.

`expr_unsafe_for`'s `ExprLambda` arm did not know that: it collected every
ident in the lambda body and treated any mention of the name as a capture,
i.e. an escape. From there the cascade is the credit machinery working
correctly on a false premise — `alias_bind_sites_of` requires the alias not
to escape, so the alias site was refused; #7282's rule that the forgiveness
and the credit-copy must agree then withdrew the SOURCE's credit too; and
with neither slot credited, neither box was released.

## The fix

The arm now tests `parser.is_value_block_origin(lm.origin)` and walks a value
block's body with the ordinary strict walker instead of the blanket capture
test. Inside an inlined block the same borrow rules apply as outside it. The
walker's own `StmtReturn` arm still flags the block's VALUE, which is the one
way a name really does leave one (`var y = match (k) { A => p, … }`), so the
narrowing gives up no soundness. A real lambda keeps the blanket test.

`if` expressions, plain block expressions and the comprehensions ride the
same origin set and are fixed with it — the poisoning was never specific to
`match`.

## Three walkers, because the bug was never one walker's

`expr_unsafe_for` is where the measurement landed, but two siblings carried
the identical blanket test for other kinds, and a grep for the pattern found
exactly them: `strarr_expr_unsafe` (string[]) and `darr_expr_unsafe` (dyn
arrays). The string[] case measures the same way — `xs[0].len()` read through
a match expression was 500 allocs / 100 frees where the plain read is
500/500 — so all three are fixed together. Shipping only the struct-side one
would have left the same bug standing under a different predicate.

`expr_view_borrow_only` also tests a lambda body by membership and is
deliberately NOT changed: it asks whether every use is a view borrow, which
a value block's body can violate, so exempting it needs its own recursive
form and its own measurement.

The string[] walker's exemption passes an empty `str_fresh`, which disables
only the self-`append` / self-`.with` rebind forgiveness inside the block. That
can refuse a credit, never grant one — a conservative bound, stated here
because it is a real (if narrow) piece of coverage given up for a smaller
signature change.

## What moved

`enum__local` flips to clean: the first `local` row in the matrix to do so
for a boxed kind, and it took both this and the field-share slice, one per
half of that compound cell. `struct__local` stays leak — its read contains no
value block, so it is a genuinely different cause and still owns a slice.
