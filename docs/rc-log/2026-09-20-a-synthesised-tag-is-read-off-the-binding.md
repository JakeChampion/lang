# 2026-09-20 — a synthesised tag is read off the binding

The fuzz census after #9825 had 52 programs the typed path refused, and
the root refusals, once the "is a function value" and "no semantic
contract" consequences are stripped, are a short list. Nineteen of the 52
were one family in three shapes, all of them the result tag a synthesised
declaration carries.

## The unary arm

`if_expr_rt` reads a value block's arms to label its IIFE, and had no arm
for a unary expression, so `(!false)`, a cast and a negation all fell to the
i32 default. Two reducers that differed in nothing but an `id(true)` for an
`id(false)` in the condition were in fact one reducer with a `(!false)` arm
and one without; the token dump found the arm, not the condition. Inside
another block's arm no binding annotation reaches the inner block, so its
own reading is all it has: `!` is a boolean, a cast names its target, a
negation has its operand's width — the reading `mono_infer` already gives.
Eight of the 52 programs (seeds 115, 120, 128, 174, 180, 388, 419 and the
`(!false)` reducer) produce whole on this arm alone.

## The binding's annotation

`stamp_value_block_ret` applied a binding's annotation to the value block
bound to it only when the annotation was `i64` or `u64`, on the grounds that
the tag only had to stop contradicting a wide body and was "never meant to
carry" a spelling like `Option[i32]`. A `None` arm shows why it has to:
`var v: Option[i32] = (if (c) { None } else { None })` has no arm the
classifier can read, and the body inference in `semsource.result_type` has
nothing either — the checker types a bare `None` as `Option` with no
payload — so the block kept its i32 label and the module was refused for
holding an i32 where an Option was declared. The block's value IS the
binding's, and the checker holds every arm to the annotation already, so
the annotation is stamped whatever it spells. Two things are left alone: a
coarse fn tag, whose signature lives in sidecars the lambda has none of,
and a spelling naming a type variable, which the instantiation substitutes
later. The AST lowering takes the `Option[i32]`-tagged IIFE as it takes a
hand-written one; `none-arm-option-binding` in the value-block width table
pins that under strict IR on both natives.

The checker had to follow. It types a value block from its arms and fell
back to the lambda's tag when they disagreed, which was harmless while the
tag was a guess the arms had nothing to do with. Stamped, the tag is what
the parser's float settle reads: in `var x: f64 = if (c) { n } else { 2 }`
the `2` settles to f64, the `n` arm stays i32, the mixed pair fell to the
f64 tag and the E003 native reports was gone (`settle-value-if-i32-arm`).
The fallback now reads the arm that did not settle — the first whose type
is not the tag's — and stays untyped when the arms fail the E031 rule, so
that mismatch is reported once, as native does.

## A family is not a type

`semtypes.concrete` counted a union with no payloads as concrete, because
`concrete_payloads([])` is vacuously true. The checker types `Some(k)` by
its family alone, so `inferred_result` answered `Option` for a lambda
annotated `Option[i32]`, `result_type` preferred that "concrete" body type
over the annotation, and the contract said `Option` — the body then failed
`return type: declared Option, returns Option[i32]`, and the caller's
`match` on the result could not name a variant. `Option` needs one payload
and `Result` two; without them the name is a family, not a type, and the
annotation stands. Seeds 198, 225, 234 and 382 produce whole on this.

## The returned lambda's slot

`return <lambda>` is desugared to `var $lamret$N = <lambda>; return
$lamret$N;` so the lambda lift boxes it like any closure local. The slot
carried no type, and once the lift replaced its initialiser with a
`__mkclo$…` box constructor nothing could type it: `unresolved type of
binding $lamret$0`, on every capturing lambda returned from a local
function (seeds 010, 204). The slot is now declared as the enclosing
declaration's return signature — the coarse tag plus its sidecars, the same
triple a hand-written `var f: (P) => R` carries — and a lambda spelling no
result of its own takes the signature's. An if/match-expression IIFE has
empty sidecars, so a lambda returned from one of its arms is still typed
from the arm alone (seeds 298, 427 — the `declared fn` bucket).

## Census

| binary | whole | agree | diverge |
|---|---|---|---|
| main after #9825 | 456 / 508 | 499 | 0 |
| this change | 476 / 509 | 500 | 0 |

One program (seed 321) the self-host checker refused before, with an E042
native does not report — a `?` inside a value block whose result the tag
misnamed — compiles on both legs now and agrees, which is the extra row. The compiler's own sources produce whole
before and after (8622 of 8622, then 8623 of 8623 with the checker's new
helper).
