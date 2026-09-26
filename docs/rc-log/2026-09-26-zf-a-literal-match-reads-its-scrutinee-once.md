# A literal match reads its scrutinee once

The self-host parser turns a `match` with literal patterns into an
if/else-if chain, and every arm's test repeated the scrutinee expression. A
call written there ran once per arm it reached, and twice for a range arm
(#10327). Both lowerings compiled it that way; native and the interpreter
evaluate it once. A statement-position match now binds any scrutinee but a
bare name to a local first. A value-position one becomes the block a
programmer would write, `{ var __lm = scrut; match (__lm) { … } }`, so the
match itself keeps the one-statement shape every value-block reader
expects. Routing its arm values through a value local instead, as the tuple
and struct desugars do, declared that local from the parser's syntax-only
tag: a closure or struct-array arm stored into an `i32`. The lifted-lambda
leaf readers (`fn_inferred_struct_ret`, `lam_leaf_array_elem`,
`lam_leaf_opt_type`) and `iife_leaf_value` now see through a block to its
last statement, which also types `{ var k = …; if (…) { [P { … }] } else
{ … } }` written directly.

The checker's value-local retyping had no spelling for a function type, so a
tuple or struct match yielding closures drew E003 at its arm stores;
`vb_annotation_of` spells it through `typeinfo.spelling`.

`true` / `false` arms now take the same desugar. The parser counted only
number and string tokens as literal patterns, so a boolean match stayed a raw
`match`, which the typed path refuses (#10323). Native requires the `_` arm
on a boolean match as on any scalar, so the desugar's E030 rule already
fits.

Caching the scrutinee put a declaration inside a value-position match, and a
lambda in a value block's declaration was never lifted: the inline-IIFE lift
walked only conditions and returned values. `lift_iife_body` now also lifts
a declaration's initialiser, an assignment and an expression statement; the
returned values stay with the arm-value boxing. That also fixes source that
writes the shape directly, `{ var q = g([(x: i32) => x + k]); … }`. Still
raw: a match arm's guard, and the bodies of a `while`, `for` or `defer`
inside a value block.

The cached scrutinee also exposed a Perceus bug on the AST lowering. In
`build_literal_match`, a local `sugar` is lent to `with_match_sugar`, whose
struct literal retains it into the result. The plan credits the lend, so the
caller's exit sweep ran the deep field walk unconditionally and freed the
enum and array the returned statement still held; the self-compiled checker
driver segfaulted on any literal match with a trailing `_`. A struct local
lent at a position not proven a plain borrow is now marked `SINKSHARE:`, so
its walk runs only at rc 1 (`struct_lent_to_retaining_call`).
`TestSelfHostStructLentRetained` pins the shape on both lowerings.

`TestSelfHostLiteralMatchScrutinee` pins the evaluation count on x86-64,
arm64 and wasm, each value checked against native. `TestSelfHostClosureArrayIR`
moves to the CLI; its `iife-scrutinee-closure-arg` case matched on a boolean
call scrutinee holding a closure.
