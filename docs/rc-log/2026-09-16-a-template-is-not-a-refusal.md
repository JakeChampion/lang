# 2026-09-16 — a template is not a refusal

Two findings from one probe, `(o: Option[T]) is_some()`.

## A generic method is a template

`unsupported generic method` was a declaration-level refusal, and a method
whose only type variables were its RECEIVER's — every method of
`std/option` and `std/result`, `(o: Option[T]) is_none()` among them —
did not even reach it: `generic_decl` read the declared type parameters
and the parameter spellings, not the receiver's, so the method was
produced as an ordinary body and refused at its first parameter,
"unresolved parameter type", with nothing saying why. The corpus held 12
such sites and 17 "unresolved result type" beside them.

A method on a generic receiver is now a template whose variables are the
names its receiver's arguments spell (`receiver_typevar`), and a call
through the nominal `Type.method` contract that carries variables goes
through `invoke_method`, the instantiation the folded array and map
methods already took: the receiver binds the variables, the instance is
requested under that binding and named by it, and the call names the
instance.

## The all-or-nothing rule counted templates

That was not enough: the probe still fell to the AST lowering, and so
did `function twice[T](x: T, f: (T) => T): T` with a plain call. The
all-or-nothing rule (#9437) counts the declarations kept, and a
template's row is never kept — its erased body is no body of the
module's, `produced_plan` refuses it as "generic template" — so a module
with any template the parser had not pre-instantiated fell back whole,
and since the refusal was the template's own it was never reported. The
fixture suites never saw it: their bisect knobs keep a mixed module.

A template with produced instances, or one nothing instantiates, is now
accounted as kept without a body; only a template with a refused
instance is a refusal, and that instance's reason is reported under the
template's name.

Corpus, whole modules produced: 384 → 405; modules on the AST lowering
137 → 116. The largest remaining reasons are `Map[string, JsonValue]`
(a union value column) and the function-value family.

## Pinned

`TestSelfHostSemanticSourceRC` runs `opt_has` (a generic method
instantiated at i32, present and absent) and `opt_words` (the same at
string, whose payload the instance hands back retained) on all four
targets under the leak check. The print golden carries `has_it`,
`or_val` and `opt_calls`, with the template verdict and the instance.
`option_is_none_or`, `option_result_and_then`, `generic_associated_fn`
produce whole and match native under the leak check.
