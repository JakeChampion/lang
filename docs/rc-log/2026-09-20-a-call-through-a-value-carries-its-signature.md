# 2026-09-20 — a call through a value carries its signature

After the closure-slot work the fuzz census had eleven programs left, eight of
them the map representation's `map unit is not shared`. The other three were
three shapes, one seed each, and each is a limit of a different layer.

## The signature slot

`function signature slot` was documented as the vocabulary's limit: the call
through a function value was the untagged `call_indirect`, which describes
every slot of the funcref as one word, so a function type with an i64 or f64
parameter or result was refused where it was declared. It was the third layer
behind seed 179: a nested function naming a sibling nested function holds the
sibling in a captured slot, and a sibling taking an i64 is a function value
with a wide slot.

The stack IR has carried a signature on that call since #6282:
`op_call_indirect_sig` names the funcref type as one width char per slot, the
tag irlower's own call sites emit and wasm dispatches through. `ssarc.signature_tag`
spells the value's type the same way — the environment word first, then the
parameters, then the result — and `function_site` emits it. The register
backends read only the arity, as they do for the AST lowering's tagged calls;
wasm names the funcref type the body was declared with. An all-narrow
signature carries no tag, so every call the path produced before is emitted
byte for byte. `ssasem.func_shape_error` keeps its two refusals — a function
type inside another, and a void result — and drops the width test.

## The nested function's spelling

Behind that, seed 179's binding was still `not yet checked`: the sibling
returns `((i32) => i32)[]`, and the lift's fn-value pass bound it as
`(i32) => fn[]`. `lambda_ret_of` read the lambda's tag, which the parser
coarsens to `fn[]` and keeps the signature beside in sidecars; the spelling
now goes through `fn_tag_spelling`, which rebuilds it. The checker had the
same gap one layer down: a lambda whose tag is `fn` or `fn[]` resolved its
result with `resolve_type`, which has no sidecars to read, where a
declaration's went through `declared_callable_type`. `lambda_ret_type` reads
the lambda's sidecars the way `func_ret_type` reads a declaration's, at every
site the checker resolves a lambda's result.

## The hoisted body's result

Seed 312 was a value block whose arm hands a capturing lambda to a template:
`(if (c) { id((x: i32) => x + k) } else { … })`. The lambda spells no result,
the lift hoists it as a declaration spelling none, and the checker's
signature table then types `__mkclo$<body>(k)` as a function returning
nothing — `undeclared_call_type` builds the box's type from the body's
signature — so `id(…)` had no type and the block's declaration no result. The
previous entry typed a SLOT holding such a box off the body's contract; a
box in argument position has no slot. `annotate_lambda_expr`, the checker
pass the lift runs first, now stamps the result a lambda's body returns
onto a lambda spelling none, in the spelling a declaration carries. Left
empty: a function-valued result, whose signature the coarse tag and its
sidecars carry and a spelling cannot, and a bare literal, which adapts to the
width of whatever receives the lambda.

## The passthrough chain

Seed 093 was `function address is not a closure value` again: a match arm
`Blue => id((match (p) { Red => [lam], … }))`, a template forwarding a
nested value block of lambda arrays. The lift boxes the arrays every arm of a
value block yields, and follows a passthrough call to the array literal it
forwards — `passthrough_array_literal` — but not to a value block, so the
nested block's lambdas were left where the plain lift turns them into bare
addresses. The chain now ends at a value block as it ends at a literal, on
the gate (`iife_arm_arrays_boxed_elems`) and the rewrite
(`box_iife_arm_array_elems`) alike, and the nested block's arms box through
the same recursion a nested block written directly takes.

## What the new cases surfaced

The nested-function case failed on every target once written: the AST
lowering, which had refused the shape by accident — the flattened spelling
left a lambda it never lifted, and `no lifted main$clo` was the bail — now
lifted it and dispatched the returned array's elements as bare addresses,
a segfault on x86-64 and a table trap on wasm. Whether the array a function
value hands back holds boxes or bare addresses is the callee's choice, and
the AST lowering reads it off a named callee's body; through a value there
is no body to read. It now refuses the call (`call through the function
value … returns a function array`), so the case is a `want` row: the
semantic path produces it whole, and the sanitizer leg reports every box
released.

Chasing the wasm trap through the driver's binary route found a second,
older gap: the no-I/O component framing called
`wasm_ir.emit_module_mode_or_error` without the substitution every other
framing passes, so a program only the semantic path produces was refused
as `not IR-eligible` when compiled to a `.wasm` file with no `print` in it,
and a program both paths lower shipped the AST lowering's bodies. The
production harness assembles `-emit asm` text and never saw it. The
framing now takes the substitution, and `emit-wasm-component` in the CLI
test carries a typed-only row.

The signature admission also moved four rows of the erased-wide array gate
(`self_host_erased_wide_array_gate_test.go`): `array.map(xs, dbl)` at f64
and the three `xs.map(…)` spellings at i64 were pinned as REFUSED on wasm,
because the module fell to the AST wasm path, which the gate keeps such
shapes off. They fell there only because the semantic lowering rejected
the wide slot in the function value's signature; produced, they answer as
the interpreter does on every target, so they join the fixed list and the
refusal list goes with its test.

## Census

| binary | whole | agree | diverge |
|---|---|---|---|
| main after #9827 | 498 / 509 | 500 | 0 |
| this change | 501 / 509 | 500 | 0 |

The eight programs left are the map representation's. Seed 179 leaves the
agreeing column: the semantic path produces it and the AST lowering now
refuses it at the call through a function value returning a function
array, where it used to refuse it one layer earlier. The compiler's own
sources produce whole (8634 of 8634).
