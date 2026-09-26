# A literal match reads its scrutinee once

The self-host parser turns a `match` with literal patterns into an
if/else-if chain, and every arm's test repeated the scrutinee expression. A
call written there ran once per arm it reached, and twice for a range arm
(#10327). Both lowerings compiled it that way; native and the interpreter
evaluate it once. `build_literal_match` now binds any scrutinee but a bare
name to a local first, and a value-position match routes its arm values
through a value local, as the tuple and struct desugars do.

`true` / `false` arms now take the same desugar. The parser counted only
number and string tokens as literal patterns, so a boolean match stayed a raw
`match`, which the typed path refuses (#10323). Native requires the `_` arm
on a boolean match as on any scalar, so the desugar's E030 rule already
fits.

Caching the scrutinee put a declaration inside a value-position match, and a
lambda in a value block's declaration was never lifted: the inline-IIFE lift
walked only conditions and returned values. `lift_iife_body` now lifts every
expression in the body except the returned values, which the arm-value
boxing owns. That also fixes source that writes the shape directly,
`{ var q = g([(x: i32) => x + k]); … }`.

`TestSelfHostLiteralMatchScrutinee` pins the evaluation count on x86-64,
arm64 and wasm, each value checked against native. `TestSelfHostClosureArrayIR`
moves to the CLI; its `iife-scrutinee-closure-arg` case matched on a boolean
call scrutinee holding a closure.
