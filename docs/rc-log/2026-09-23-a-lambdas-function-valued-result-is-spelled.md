# A lambda's function-valued result is spelled

## What it was

A lambda bound to a local that returns a lambda, with no result written
(`var mk = () => { return (b: i32): i32 => b * 2; };`), was hoisted with an
empty result: `checker.inferred_lambda_ret` stamped the returned value's
spelling, and a function type has none, since its signature lives in the
`ret_fn_ret` / `ret_fn_param_types` sidecars. The signature table then typed
`mk()` as nothing, and the call of the call, `mk()(3)`, was refused on the
typed path ("callee is neither a name nor a field"). That is the first half of
#10025.

## What it does

`inferred_lambda_result` returns the lambda with its result filled: a scalar
or record result as before, and a function-valued one as `fn` with the
sidecars `decltypes.fn_param_from_type` builds from the returned value's type,
the same pair a source function's written callable result carries. A result
that nests a function inside another type is still left empty, since no
spelling carries it.

## Measured

`a-lambda-returning-a-lambda-is-called-through-its-result` (two capture-free
returned lambdas, one called through a local and both called through a call):
5 of 5 declarations produced, answer 42 on every target, matching native. The
previous compiler produced 0 of 5.

## What it does not reach

A CAPTURING returned lambda (`var curry = (a: i32) => { return (b: i32): i32
=> a + b; };`) now types its call of a call, and is refused one step later:
the lifted `__lam_N` body's tail lambda keeps `hoist_escaping_closure`
(`unsupported expression`), because given the `$lamret$N` slot instead the AST
lowering segfaults on that shape (#5281). That is #10025's second half.
