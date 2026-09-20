# 2026-09-20 — a superseded template has no body

`xs.map(dbl)` over `i64[]` compiled for wasm was refused as `not IR-eligible`
with no bail site named (#9838), while the typed path reported every
declaration produced and the free spelling `array.map(xs, dbl)` built. The
parser folds the method call into the free generic `__arrm_map[T]` and the
worklist clones `__arrm_map__i64`, whose `U` stays erased; the semantic
lowering declines the clone as a generic template and produces its instance,
and `ircore.produced_or_lowered` then fell back to the AST lowering of the
erased clone body. That lowering's `erased_wide` verdict — a wide value
through an erased parameter, which the wasm backend types i32 — was the
module's only one, and `wasm_ir.module_erased_wide` declined the module on
the strength of a body nothing in it calls.

With the flag ignored every variant (i64 and f64 element, named function
and lambda, wide and narrow result) loaded and answered as the interpreter
does, so the dead body's WAT was valid and the verdict was the whole of the
refusal. The fix is the rule the decline already stated: a template's erased
body is no body of this module's. In a module produced whole,
`semlower` replaces the template's entry with a superseded one
(`irlower.LowerResult.superseded`, no ops) and the three emitters skip it,
as the wasm one skips an `@import` extern; nothing calls it, since every
produced caller calls an instance. Under the bisect knobs a module is mixed
and an AST-lowered caller may call the template, so there its AST lowering
stands, verdict included.

Rows: `TestSelfHostErasedWideArrayFixedWasm` gains the i64 element under the
method spelling (named, lambda, narrow result) and the f64 one;
`TestSelfHostCLIX86_64/emit-wasm-component` gains `i64-map-method`, the
issue's reproducer through the driver's `-o` route. The register backends
lose the dead template bodies from their output, which is the only change
there.

The compiler's own sources produce whole (8648 of 8648), the fixture corpus
through the self-host compiler is green, and the semantic whole-compiler
gate (`TestSelfHostSemanticWholeCompilerX86_64`) passes with the dead
template bodies gone from its output.
