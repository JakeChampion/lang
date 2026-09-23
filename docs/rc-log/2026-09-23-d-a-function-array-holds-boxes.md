# A function array holds env boxes, whatever built it

## What it was

The AST lowering had two representations for a `fn[]` value. An array literal
of named functions (`[a, bb]`) held bare code addresses and dispatched
`call_indirect(argc)`, while an array holding a lambda held `__mkclo$` env boxes
and dispatched env-first. Every other function value was already a box: a
returned named function (a static box), a returned lambda (the `$lamret$N`
slot), a function-typed parameter, a boxed local.

Keeping the two apart took a registry at every place an array could come from:
the `is_fnarr` slot flag, the whole-program `FNPTR:` / `CLOARR:` / `FNFLD:`
field scan, the `'3'` closure-array parameter proof, four rules that promoted a
literal to boxes when a sibling return, store, argument or if/match needed them,
and `check_fn_array_fields`, a compile error for a field whose construction
proved neither representation. Wherever an element left its array without
passing one of those rules, it met a caller that assumed the other
representation and segfaulted. #10076's repro was `return fs[i]` from a
function declared to return a function, on both lowerings, since the typed path
refused the module ("function address is not a closure value") and fell back.
A rebind between named functions and a lambda (`[seven]` then `[() => n]`) was
the recorded open half of the same defect.

A zero-parameter function was never boxed as an array element, because the lift
and `const_fns_of` treated every zero-parameter function as a `const`. The
declaration carries `is_const`, so the guess was unnecessary.

## What it does

The lift boxes every function value in an array literal. A lambda boxes (a
`$wrap` trampoline when it captures nothing), a named function boxes through its
`$wrap`, and a value-position if/match element whose arms all box becomes a call
yielding one. A bare name is a function value unless its declaration is a
`const` (`lift_names_fn_value`, and `const_fns_of` reads `is_const`). So a `fn[]`
slot, field, parameter and return are closure arrays by their type alone.

Deleted: the `is_fnarr` flag and every branch on it, the fn-pointer struct
literal and binding and append and rebind paths, the field scan and its three
markers, `check_fn_array_fields` and its driver calls, the `'3'` parameter proof
(`closurearr_registry_of`, `fnarr_param_all_closurearr`), the four promotion
rules and the forced-boxes argument rule, the annotation-driven zero-parameter
wrappers for tuples and Option/Result payloads, `lift_arg_is_fn_value_declared`,
`lift_module_fn_arity`, and the parser's `infer_fnvalue_locals_module`, which
existed to stop `var f = mk` reading as a const call.

The struct-drop walk already classified a `fn[]` field as an element-walked box
array (the coarse `fn` reads as enum-like), with the `FNPTR:` marker opting a
pointer field out. With no pointer fields left, every `fn[]` field takes the walk
closure-array fields took.

## Measured

`a-function-array-holds-boxes` (a returned element of a named-function array and
of a zero-parameter one, an empty annotated `fn[]` reassigned from a parameter, a
method's `fn[]` parameter, zero-parameter functions in a tuple, an Option and an
enum payload): 19 of 19 declarations produced, answer 64 on both lowerings and
native, absolute leak pin. The #10057 build segfaulted on it on the AST
lowering. `conformance/cases/function_array_element_returned` answers 60 on
every leg; its leak-census row is 0. The three cross-representation rebinds join
`self_host_fnptr_array_rebind_test.go` and answer 5, 7 and 5; the #10057 build
segfaulted on each. The closure-call census's function-array case moves from two
`plain` sites to two `env_other` sites. A sweep of the conformance corpus,
`examples/` and the e2e testdata (196 programs) on both lowerings found no
function-pointer array before the deletion.
