# 2026-09-16 — an unannotated result is the first returned value

A hoisted lambda carries no result annotation — `((lo, hi): (i32, i32)) =>
hi - lo` lifts to `__lam_1` with an empty `ret_type` — and `result_type`
read an empty spelling as `void`, so every such body refused as "return
type: declared void, returns i32" and its caller lost the contract. The
AST lowering never needed the type: `infer_ret_types_module` guesses one
syntactically and every slot is 8 bytes anyway. This boundary needs the
checker's.

A declaration spelling no result now has the type the checker gives the
first value it returns: the body is walked through its branches, arms and
loops for the first `return` with a value, threading `checker.bind_stmt`,
`arm_scope` and `for_body_scope` so the expression is typed in the scope
the statements before it built. A body returning no value stays void. A
return whose type the checker cannot spell in that scope — a tuple
literal, a binding holding a function value — refuses as before,
"unresolved result type", which is what `lambda_tuple_return` and
`use_callback_bind` still say.

`param_destructure` and `param_destructure_struct` produce whole and
match native under the leak check.

## Pinned

`TestSelfHostSemanticSourceRC` runs `lam_inferred` (an integer lambda
with no result annotation) and `lam_text` (one building a fresh string
and returning its length) on all four targets under the leak check; the
print golden carries `inferred_plain` and `inferred_ret`, the latter a
capturing body whose returned value follows a declaration.
