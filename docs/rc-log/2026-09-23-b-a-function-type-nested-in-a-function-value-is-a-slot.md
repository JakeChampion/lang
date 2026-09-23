# A function type nested in a function value's type is a slot

## What it was

`ssasem.signature_slot` refused any function type inside another: a
function VALUE whose parameter or result is itself callable failed
semantic verification with `function signature slot` (#10024). So a
higher-order function could be called by its declaration's name, where
the callback parameter is a contract, but not through a local, a
parameter, or a `use` through a local.

## What it does

A function type is one word, the environment box every function value
is, whatever it nests, and the call through a value dispatches by
`ssarc.signature_tag`, which already spells that word `w`. So a
parameter or result of function type is a slot when its own signature
is: `signature_slot` recurses through `func_shape_error` for a function
type and keeps refusing a function nested in an array, tuple or map
(`nests_func`), where no release walk reaches it yet.

## Measured

`a-function-value-whose-type-nests-a-function-type`: a local bound to a
function taking a callback, a `use` through such a local, a parameter
whose type takes a callback, and a local bound to a function returning a
function value. 17 of 17 declarations, answer 41 on every target, the
absolute leak pin on the sanitizer leg. The previous compiler produced
0 of 17.

The typed path is the CLI's default, so this also fixes a crash in a
default build: calling the result of a call through a function value
(`mk(10)(20)`, `mk` a fn-typed local or parameter) segfaulted on main,
because the whole module fell back to the AST lowering, which dispatches
the returned closure box as a bare code pointer (#10057). The typed path
answers 30, as native does. `conformance/cases/call_of_a_call_through_a_function_value`
pins it on every leg; its leak-census row is native's 6 unpaired
allocations.

## Traps

The production suite's control leg is the AST lowering, which segfaults
on that call of a call, so the row binds the result first
(`var add10 = mk(10); add10(0)`) and the conformance case carries the
same-expression form. #9954's row (`a-lambda-declaring-a-callable-result-over-a-generic-struct`)
pinned `function signature slot` as the refusal that stands; it now
stops one step later, at the capturing lambda its `outer` returns
(`outer$wrap0: unsupported expression`, #10025's second half).
