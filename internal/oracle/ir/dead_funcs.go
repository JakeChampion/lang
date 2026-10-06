// Dead-function elimination at the IR level.
//
// Treeshake runs at the AST level before lowering, so it removes
// helpers no user code references. After IR-level inlining +
// defunctionalisation + closure-pair elision, more functions
// become unreferenced — the inliner spliced their bodies into
// every caller, the defunctionaliser rewrote indirect dispatches
// to direct calls (potentially leaving the original closure
// target as the sole reference, then losing that too once the
// hoisted body got inlined too). This pass picks up those
// orphans.
//
// Reachability:
//   - main, handle (entry points the wasm exports) and any
//     names listed in `keepAlive` (PrintMainResult's
//     `int_to_string`, etc.) are roots.
//   - From each reachable function's body, every OpCallDirect /
//     OpCallClosureDirect / OpMakeClosure / OpMakeEnv /
//     OpConstFunc keeps its target alive.
//   - Codegen-level call-name aliases (`map_new` →
//     `map_new_impl`, `__array_append_jsonvalue` →
//     `__array_append_string`, etc.) are followed via the
//     `aliases` map so the impl bodies survive when only the
//     IR-side name is referenced.
//   - Iterate to fixpoint: a freshly-marked function may itself
//     reference more functions.
//
// LiveFunctions returns the reachable set; the caller is
// responsible for filtering the IR program in a way that
// preserves any AST-side bookkeeping (the wasm codegen's
// scan-for-uses passes still walk the original AST, so
// callers should NOT trim prog.Funcs in lockstep — only the
// IR's emission is gated, codegen iterates ip.Funcs and looks
// up matching FuncDecls by name).

package ir
