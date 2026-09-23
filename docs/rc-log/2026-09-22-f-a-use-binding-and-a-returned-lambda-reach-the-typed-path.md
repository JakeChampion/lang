# A use binding and a returned lambda reach the typed path

2026-09-22. Two lift shapes the typed producer refused: the continuation a
`use` binding desugars to, and a lambda returned from inside a lambda. Three
corpus cases move to the typed path.

## What it was

`use v <- f(args)` desugars the rest of its block into a callback lambda whose
parameter has no type: the checker infers it from the callee's signature (the
E032 rule) but recorded nothing on the tree, so the `$wrap` trampoline the
lift built from a capture-free continuation declared an untyped parameter and
the producer refused it — `main$wrap0: unresolved result type: inferred from
the first returned value`, since `return v` had no type either. That held
`use_callback_bind` (3 declarations) and, through the `bound` lambda,
`arrow_lambda_block_body` (19).

A lambda returned from inside a lambda stayed a bare function name. The
pre-worklist pass rewrites a source function's `return <lambda>` into a
`$lamret$N` slot the closure lift boxes, but a lifted `__lam_N` body never got
it (#5281 withheld it), so the lift's return arm hoisted the inner lambda and
returned its address, which the producer refuses as a closure value
(`function address is not a closure value`). That held `lambda_tuple_return`
(6).

## What it does

`checker.pretype_module` runs ahead of both `check_module` and
`annotate_module` (it is the value-block retype pass with a second rule): a
call whose last argument is a `use_infer` lambda with an untyped first
parameter takes the type of the callee's trailing callback parameter's first
parameter — from a function-typed local first, since a binding shadows a
module function of the same name, else from the module signature — and
stamps it on the lambda before either pass binds the body's scope. The body
then reads a typed binding, `inferred_lambda_ret` answers, and the trampoline
declares both; the checker reports E003 on a mistyped read of the binding as
native does. A callback parameter that is itself a function type is stamped
as `fn` with its sidecars (`decltypes.fn_param_from_type`); a type with no
declaration spelling leaves the binding as written. (`SELFHOST-CHECKER-PORT.md`,
same date, has the pass.)

`irlower.desugar_lifted_lambda_returns` runs at the top of the worklist drain,
so every lifted body gets the `$lamret$N` rewrite a source function got before
it: a no-op for a source function, whose pass already ran. A CAPTURING tail
lambda stays for `hoist_escaping_closure`: given the slot instead, the AST
lowering segfaulted on the `curry` shape (a capturing lambda returned from a
lambda bound to a local), which is #5281 exactly, so that shape keeps its old
route and its old answer.

## Measured

x86-64, typed path, produced whole, exit as expected:

| case | declarations | exit |
| --- | ---: | ---: |
| `use_callback_bind` | 3 of 3 | 42 |
| `arrow_lambda_block_body` | 19 of 19 | 0 |
| `lambda_tuple_return` | 6 of 6 | 0 |

Three production rows (`TestSelfHostSemanticProduction`), each with an
absolute leak pin on the sanitizer leg and run on the wasm leg:
`use-binding-takes-the-callee-parameter-type` (a capture-free and a capturing
continuation over a string binding, 200 rounds, 1400 allocs and 1400 frees,
109 of 109), `lambda-returns-a-lambda-from-a-lambda` (two annotated
lambda-returning lambdas, one called through a local and one called directly
as `mk2()(i)`, 5 of 5) and `block-bodied-lambda-with-a-use-binding` (5 of 5).

## What it does not reach

A capturing lambda returned from a lambda (`var curry = (a: i32) => { return
(b: i32): i32 => a + b; }`) keeps the AST lowering: the hoist route stays for
the reason above, and its caller's `curry(1)(2)` is refused anyway, since the
checker types a lambda's function-valued result as nothing ("sidecars a
spelling has no room for") and the call of a call needs a function type to
dispatch (#10025). A function value whose own type nests a function type is refused by
the verifier as a slot (`function signature slot`, #10024), so a `use`
through such a local cannot reach the typed path yet; the annotate stamp
covers it all the same.
