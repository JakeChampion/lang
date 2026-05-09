// Closure defunctionalisation at the IR level.
//
// Rewrites OpCallIndirect → OpCallClosureDirect for closure-call
// sites whose receiver slot has a single MakeClosure flow source
// (the "monomorphic flow" case from Roc's lambda-set
// defunctionalisation). The replaced call drops the
// function-table indirection — wasm's `call_indirect` becomes a
// plain `call` to the hoisted target — saving an i32.load of the
// fn_idx + the table dispatch + the runtime type check
// `call_indirect` performs against the recorded sig.
//
// Recognised pattern (closureconv's well-known shape):
//
//   ... evaluate captures ...
//   OpMakeClosure name
//   OpStoreLocal slot
//   ... ...
//   ... evaluate args ...
//   OpLoadLocal slot
//   OpCallIndirect
//
// If `slot` is written exactly once, by an OpStoreLocal directly
// preceded by OpMakeClosure with target `name`, the
// OpLoadLocal+OpCallIndirect pair becomes:
//
//   OpLoadLocal slot
//   OpConstI32 4
//   OpAdd
//   OpLoad                              ; env_ptr from closure pair
//   OpCallClosureDirect name argc=N+1   ; +1 for env_ptr
//
// Slots written multiple times, or written from non-MakeClosure
// sources, fall through to the existing OpCallIndirect path.
//
// The pass is conservative — anything it can't statically prove
// to be monomorphic stays as-is. That's fine: indirect dispatch
// keeps working and the perf win is just left on the table for
// those sites. Future work: full lambda-set tagged-union
// dispatch for the 2..N flow cases.

package ir

// Defunctionalise rewrites monomorphic closure-call sites in
// each function. Programs without closures are unchanged in O(N)
// walk time (the per-function pass returns immediately when no
// MakeClosure ops are present).
//
// Order in the production pipeline: Lower → Inline → Fold → DCE
// → Defunctionalise → emit. Running after Inline lets the
// defunctionalised direct call ALSO get inlined if the hoisted
// target qualifies; running before Fold/DCE would leave the
// scratch arithmetic in a less foldable shape.
func Defunctionalise(prog *Program) {
	for _, fn := range prog.Funcs {
		defunctionaliseFunc(fn)
	}
}

func defunctionaliseFunc(fn *Func) {
	// Phase 1: identify monomorphic closure slots.
	//
	// monoSlot[slot] = target name when the slot is written
	// exactly once by an OpStoreLocal preceded by OpMakeClosure
	// with that target. Slots in `polySlot` are disqualified
	// (multiple writers, or writers from non-MakeClosure
	// sources) and kept out of monoSlot.
	monoSlot := map[int32]string{}
	polySlot := map[int32]bool{}
	hasMakeClosure := false
	for i, op := range fn.Ops {
		if op.Kind == OpMakeClosure {
			hasMakeClosure = true
		}
		if op.Kind != OpStoreLocal && op.Kind != OpTeeLocal {
			continue
		}
		slot := op.I32
		if polySlot[slot] {
			continue
		}
		// OpTeeLocal leaves the value on the stack AND writes
		// the slot — same write-side semantics as OpStoreLocal
		// for the purposes of flow analysis.
		isMakeClosureSource := i > 0 && fn.Ops[i-1].Kind == OpMakeClosure
		if !isMakeClosureSource {
			polySlot[slot] = true
			delete(monoSlot, slot)
			continue
		}
		target := fn.Ops[i-1].Str
		if existing, seen := monoSlot[slot]; seen && existing != target {
			polySlot[slot] = true
			delete(monoSlot, slot)
			continue
		}
		monoSlot[slot] = target
	}
	if !hasMakeClosure || len(monoSlot) == 0 {
		return
	}

	// Phase 2: rewrite OpLoadLocal+OpCallIndirect pairs whose
	// receiver is a monomorphic slot. The IR builder always
	// emits OpLoadLocal of the receiver immediately before
	// OpCallIndirect (no other ops between them — the args were
	// pushed earlier), so a one-pass scan suffices.
	out := make([]Op, 0, len(fn.Ops))
	for i := 0; i < len(fn.Ops); i++ {
		op := fn.Ops[i]
		if op.Kind == OpLoadLocal && i+1 < len(fn.Ops) && fn.Ops[i+1].Kind == OpCallIndirect {
			if target, ok := monoSlot[op.I32]; ok {
				// Inline-load env_ptr from closure_pair+4 and
				// dispatch directly to the hoisted target.
				// argc grows by 1 because env_ptr is now an
				// explicit arg (it was implicitly passed via
				// the function-table sig before).
				origArgc := fn.Ops[i+1].I32
				out = append(out, op) // OpLoadLocal slot
				out = append(out, Op{Kind: OpConstI32, I32: 4})
				out = append(out, Op{Kind: OpAdd})
				out = append(out, Op{Kind: OpLoad})
				out = append(out, Op{Kind: OpCallClosureDirect, Str: target, I32: origArgc + 1})
				i++ // skip the original OpCallIndirect
				continue
			}
		}
		out = append(out, op)
	}
	fn.Ops = out
}
