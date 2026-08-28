# The ELB tier admits the match-EXPRESSION element read

The lead the extract-then-die entry left open, closed. A statement-form
`match (src[0])` balanced while its EXPRESSION sibling — `return (match
(src[0]) { … }) + i` — leaked, one token apart, because the value-position
desugar wraps the body in a zero-param IIFE: the walker met an `ExprLambda`
whose capture set holds the param and took the lambda-capture escape, which
reads a captured name as carried out of the frame entirely.

It is not carried anywhere. `is_iife_callee`'s own argument supplies the
proof: `lower_iife` lowers such a body INLINE in the enclosing frame and
builds no env box, so the names it reads are the enclosing function's own
locals. An element read inside the IIFE is therefore the same read the
statement form makes and earns the same admissions.

`arrenum_param_esc_expr` is the expression half of the flag-question walker.
It widens `arrenum_esc_expr` in exactly one place — an `is_iife_callee` call,
whose lambda body recurses into `arrenum_param_escapes` — and delegates every
other form to the stricter walker, so the widening cannot reach past this
shape. The four statement VALUE positions (var init, assign value, return,
expression statement) route through it; conditions and iterators keep the
strict walker, where an IIFE would be a different question.

## Measured (rc-payload `E { A(i32[]), B }`, producer keep, 100 rounds)

| shape | before | after |
|---|---|---|
| `return (match (src[0]) { … }) + i` | 4/2, 80 live | **4/4, 0** |
| statement-form sibling (control) | 4/4 | 4/4 |
| extract-local, alias, literal, scalar flavors (controls) | clean | clean |
| IIFE handout — `return (if (…) { H { e: src[0] … } })` | refused | refused (1000/400, exit 25 = native) |
| IIFE arm binding to a call argument | refused | refused (4/2, safe leak) |
| the three statement-form handout fences | refused | refused |

Sanitize on the newly granted shape: zero findings, exit unmoved.

## A probe that is not a fence

`return (match (src[0]) { E.A(xs) => xs, E.B => [i] })` — an arm returning the
payload from an EXPRESSION match — never reaches this tier: the IR path bails
it as an "immediately-invoked value block", identically on stock main. It
looks like an ELB refusal and is not one. The compiling fence for that
question is the arm binding reaching a call argument, above.

## Gates

Both borrowed-arg suites (three new rows: the flipped expression form and the
two IIFE fences), every ArrEnum/ArrStruct/ScalarEnumArr suite, all four
matrices with no row moved, rc corpus both legs, census, lint, and
`TestSelfHostStage2FixpointArm64` per the exit-sweep-credit rule.

## Next lead

The array-returning match-expression bail above is a genuine IR-path gap
(`immediately-invoked value block`), unrelated to rc: an `if`/`match`
expression whose value is an ARRAY refuses to lower at all. That is the
next thing in this neighbourhood worth a look.
