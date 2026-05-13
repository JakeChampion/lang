// Inline-zero-capture-closure rewrite: an OpMakeClosure with
// zero captures is structurally identical to a function value
// (OpConstFunc) — both produce a `{fn_ptr, env_ptr=0}` pair
// pointer. OpConstFunc materialises that pair as a static cell
// (`.rodata` cell on natives, `closuresBase + 8*tableIdx`
// offset on wasm) without any runtime allocation; OpMakeClosure
// with zero captures still calls `__lang_alloc(8)` on wasm and
// `__lang_alloc(16)` on natives to build the same pair on the
// heap. Rewriting collapses the runtime alloc to a static
// pointer load.
//
// `ElideClosurePair` already catches the case where the value
// is consumed via OpCallClosureDirect (direct call back through
// the closure pair). This pass catches the orthogonal case
// where the value escapes — e.g. passed as a function-typed
// argument to another function (`tryThing(my_closure)`) — and
// the call site needs a real pair pointer.

package ir

// InlineZeroCaptureClosures rewrites every `OpMakeClosure`
// with `I32 == 0` (zero captures) to an `OpConstFunc` that
// targets the same hoisted function. The two ops produce the
// same runtime value (a function-value pointer) but
// OpConstFunc materialises it via a static cell instead of a
// heap alloc. Safe because:
//
//   - On every backend OpConstFunc emits a pair-pointer
//     identical in layout to what OpMakeClosure(0) emits — the
//     downstream OpCallIndirect / OpCallClosureDirect path
//     doesn't tell them apart.
//   - The hoisted closure target is already in the function
//     table (Defunctionalise + closure-conv arranged for it
//     when the program was scanned for indirect calls), so
//     OpConstFunc's `tableIndex[op.Str]` lookup succeeds on
//     wasm. On natives the static-cell registration is lazy at
//     OpConstFunc emit time.
//
// Designed to run AFTER `ElideClosurePair` (so the direct-call
// path has already collapsed its pair allocs) and BEFORE the
// final code-gen emit, in the same slot as the other linear
// IR rewrites. Programs without any `OpMakeClosure` walk in
// O(N) time and emit zero changes.
func InlineZeroCaptureClosures(prog *Program) {
	for _, fn := range prog.Funcs {
		for i := range fn.Ops {
			op := &fn.Ops[i]
			if op.Kind == OpMakeClosure && op.I32 == 0 {
				op.Kind = OpConstFunc
				op.I32 = 0
			}
		}
	}
}
