# 2026-09-16 — a value block runs its leading statements in place

After the unreachable end and the guards (#9451), `unsupported value block:
3 statements` sat at 18 corpus sites, every one a match EXPRESSION over a
tuple, a struct or a nested pattern.

## What it was

The tuple, struct and nested-pattern desugars cannot route an arm's value
through `return`: `build_tuple_match` splices each arm body into a
done-flag if-chain, where a `return` would leave the enclosing function.
So `parse_match_expr` names a value LOCAL (`__tm<line>_<col>_r`) the arms
assign, and the value block the parser builds is three statements: the
local's declaration, the chain, and `return <local>`. The checker retypes
the declaration from the stores when the parser's syntactic guess was
wrong (`vb_retype`). The semantic lowering produced only a one-statement
block — the if-expression's `if`, or a scalar match — and refused the
rest, which under the all-or-nothing rule kept the module on the AST
lowering.

## What changed

`semsource.value_block` runs a block's leading statements in the enclosing
block, in a scope of their own, and takes the last statement's value: an
`if` or a `match` as before, or a `return` read the way an if-expression's
arm reads its own. The scope is left after the value is read, so the local
and the chain's flag and cached scrutinee die there. A block whose leading
statements leave no live edge is refused; nothing in the corpus does that.
A general `{ … }` body — `{ var m: i32 = n + 1; m * 2 }`, whose parser
desugar ends in a `return` — is produced by the same rule.

Corpus, whole modules produced: 361 → 365; the reason is gone from the
histogram.

## Pinned

`TestSelfHostSemanticSourceRC` runs `tm_word` (a tuple match expression
with a string payload and a literal element), `tm_sum` (a tuple match
expression with a guard, in a loop), `tm_words` (a string result from a
tuple match expression built in a loop) and `sm_pick` (a struct-pattern
match expression) on arm64, x86-64, the sanitiser and wasm under the leak
check. The print golden carries `tm_line` and `block_expr`, which had
pinned the two-statement refusal.

## Traps

**The RC fixture's main is AST-lowered, and a string a produced callee
returns to it is never freed.** `print(tm_word(4))` from main leaked the
fresh `"elem!"`; the same program compiled whole is leak-free, and the
same leak reproduces with the AST lowering alone (`FERN_SEM_IR=` on
`print(mk(4))` where `mk` builds its result: one block). So the fixtures
that return a string are read through a produced caller (`tm_show`,
`sm_show`) that prints it and returns its length. The AST lowering's leak
is the one #9450 records on the argument side.
