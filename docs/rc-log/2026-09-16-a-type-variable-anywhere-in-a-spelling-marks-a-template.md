# 2026-09-16 — a type variable anywhere in a spelling marks a template

`xs.map((x: i32): string => …)` refused its module: the parser's
instance of `map[T, U]` at the receiver's element, `__arrm_map__i32`,
keeps `U` in its result `U[]` for the function argument to bind, and
`generic_decl` looked for a BARE variable letter in the result and the
parameters, which `U[]` is not. The instance was built as an ordinary
body, its result "unresolved result type", and its callers had no
contract to call.

A type variable anywhere in a spelling — bare, an array's element, a
function type's slot, a receiver's argument — now marks the declaration
a template (`spelled_typevar`, which also took over the receiver rule of
the day before). The instance is a template with the variable `U`, a call
binds `U` from the function argument's own type, and the call names
`__arrm_map__i32$string`. `generic_array_method_chain` and
`user_fn_shadows_stdlib_param` produce whole and match native under the
leak check.

## Pinned

The shape exists only after the parser's monomorphisation, which the RC
harness does not run (its AST-lowered main must lower the whole program,
and an unmonomorphised `xs.map_to(f)` does not), so the self-host fixture
legs are the gate: `generic_array_method_chain` and
`user_fn_shadows_stdlib_param` run through the semantic lowering there.
The print golden carries `mapped_to`, a generic array method with a
variable of its own bound from its lambda argument.
