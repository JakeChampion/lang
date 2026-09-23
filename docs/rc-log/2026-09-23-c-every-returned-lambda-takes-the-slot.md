# Every returned lambda takes the `$lamret$N` slot

## What it was

A `return <lambda>` is rewritten to `var $lamret$N = <lambda>; return
$lamret$N;` so the closure lift boxes the lambda through its local binding.
Source functions got that everywhere. A LIFTED body (`__lam_N`, `$cloN`) and a
body spliced out of a sole `return (…)()` IIFE got it on every return except a
tail one whose lambda CAPTURES: that one was left for `hoist_escaping_closure`,
which hoisted the lambda to `<fd>$clo` and left the lambda itself at the return
site for `lower_expr_lambda` to box. The split was #5281's: given the slot
instead, the AST lowering segfaulted on the `curry` shape.

The typed path cannot read a lambda left at a return site, so a capturing
lambda returned from a lambda bound to a local refused (`__lam_N: unsupported
expression`), the whole module fell back to the AST lowering, and there a
three-level curry (`c3(1)(2)(3)`) segfaulted. That is #10025's second half.

## What it does

The #5281 segfault no longer reproduces: with every tail on the slot form, the
return-closure, nested-return-closure, IIFE-arm, lambda-lift and every other
closure test on the self-host passes, and so do the self-host fixture legs, with
the hoist disabled outright. So the split is gone: `desugar_lifted_lambda_returns`
and `unwrap_sole_iife_return` run the full desugar, and what served only the
hoist is deleted — `hoist_escaping_closure`, `desugar_nontail_lambda_returns`,
irlower's `closure_captures`, the `clo_base` frame field, and the body of
`lower_expr_lambda`, which now only refuses a lambda no lift claimed.

Native refused the three-level shape too (`ir: indirect call from
non-identifier expression`): `callReturnType` resolved a named or captured
callee's result but not a call's, so a call of a call of a call lost its
function type. It recurses through a call-valued callee now.

## Measured

`a-lambda-returning-a-capturing-lambda` (two levels, three levels, and a lambda
bound to a local returning an expression-bodied lambda): 5+ declarations
produced, answer 42 on every target, absolute leak pin. #9954's row
(`a-lambda-declaring-a-callable-result-over-a-generic-struct`) produces whole
for the first time (5 of 5); it had pinned a refusal since it landed.
`conformance/cases/lambda_returning_a_capturing_lambda` answers 18 on native,
the interpreter, wasm and the three self-host legs; main's self-host segfaulted
on it in a default build.
