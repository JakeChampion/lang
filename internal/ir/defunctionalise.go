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
// Two flavours of "monomorphic flow source" are recognised:
//
//   1. Direct MakeClosure: `var f = MakeClosure(T, [...])`. The
//      slot's writer is an OpStoreLocal directly preceded by
//      an OpMakeClosure with target T.
//
//   2. Closure-factory return: `var f = makeAdder(7)` where
//      makeAdder is a function that always returns a closure
//      with the same target T (analysed in a phase-0 pre-pass
//      below). Covers Roc's "closure factory" pattern.
//
// Order in the production pipeline: Lower → Inline → Fold → DCE
// → Defunctionalise → emit. Running after Inline lets the
// defunctionalised direct call ALSO get inlined if the hoisted
// target qualifies; running before Fold/DCE would leave the
// scratch arithmetic in a less foldable shape.
func Defunctionalise(prog *Program) {
	returns := analyseReturnTargets(prog)
	for _, fn := range prog.Funcs {
		defunctionaliseFunc(fn, returns)
	}
}

// analyseReturnTargets scans each function in the program and
// records which closure target it returns, when there's exactly
// one. Result keys are function names; values are the
// MakeClosure target name. Functions with multiple return
// targets, no return, or return a non-closure value are absent
// from the map.
//
// Per-function analysis: walk the body and find every OpReturn
// (or fall-through end). For each, look at the value source.
// Two recognised shapes:
//   - OpLoadLocal slot, where slot is locally monomorphic
//     (writer was OpMakeClosure target=T). The function returns
//     target T.
//   - OpMakeClosure target=T directly (no intermediate slot).
//     Same conclusion.
//
// If all return value sources resolve to the same target T, the
// function is "monomorphic-returning T". Mismatched or
// unrecognised sources disqualify the function.
func analyseReturnTargets(prog *Program) map[string]string {
	out := map[string]string{}
	for _, fn := range prog.Funcs {
		if t, ok := returnTargetFor(fn); ok {
			out[fn.Name] = t
		}
	}
	return out
}

func returnTargetFor(fn *Func) (string, bool) {
	// Local monomorphic slots in fn (subset of what
	// defunctionaliseFunc's phase-1 computes — kept minimal
	// here since we only need single-target uniqueness, not
	// per-call-site rewrite info).
	monoSlot := map[int32]string{}
	polySlot := map[int32]bool{}
	for i, op := range fn.Ops {
		if op.Kind != OpStoreLocal && op.Kind != OpTeeLocal {
			continue
		}
		slot := op.I32
		if polySlot[slot] {
			continue
		}
		if i > 0 && fn.Ops[i-1].Kind == OpMakeClosure {
			t := fn.Ops[i-1].Str
			if existing, seen := monoSlot[slot]; seen && existing != t {
				polySlot[slot] = true
				delete(monoSlot, slot)
				continue
			}
			monoSlot[slot] = t
			continue
		}
		polySlot[slot] = true
		delete(monoSlot, slot)
	}

	target := ""
	for i, op := range fn.Ops {
		if op.Kind != OpReturn {
			continue
		}
		// Value source for this return — the op directly
		// preceding it. Recognised shapes:
		//   OpMakeClosure T → target=T
		//   OpLoadLocal slot, slot ∈ monoSlot → target=monoSlot[slot]
		if i == 0 {
			return "", false
		}
		prev := fn.Ops[i-1]
		var t string
		switch prev.Kind {
		case OpMakeClosure:
			t = prev.Str
		case OpLoadLocal:
			tt, ok := monoSlot[prev.I32]
			if !ok {
				return "", false
			}
			t = tt
		default:
			return "", false
		}
		if target == "" {
			target = t
		} else if target != t {
			return "", false
		}
	}
	if target == "" {
		return "", false
	}
	return target, true
}

func defunctionaliseFunc(fn *Func, returns map[string]string) {
	// Phase 1: identify monomorphic closure slots.
	//
	// monoSlot[slot] = target name when the slot is written
	// exactly once by either:
	//   (a) OpStoreLocal directly preceded by OpMakeClosure T
	//   (b) OpStoreLocal directly preceded by OpCallDirect F
	//       where F is in `returns` with target T (closure
	//       factory pattern).
	// Slots in `polySlot` are disqualified (multiple writers,
	// writers from non-recognised sources, or writers
	// resolving to inconsistent targets).
	monoSlot := map[int32]string{}
	polySlot := map[int32]bool{}
	hasFlowSource := false
	for i, op := range fn.Ops {
		if op.Kind == OpMakeClosure {
			hasFlowSource = true
		}
		if op.Kind == OpCallDirect {
			if _, ok := returns[op.Str]; ok {
				hasFlowSource = true
			}
		}
		if op.Kind != OpStoreLocal && op.Kind != OpTeeLocal {
			continue
		}
		slot := op.I32
		if polySlot[slot] {
			continue
		}
		var target string
		var resolved bool
		if i > 0 {
			switch prev := fn.Ops[i-1]; prev.Kind {
			case OpMakeClosure:
				target = prev.Str
				resolved = true
			case OpCallDirect:
				if t, ok := returns[prev.Str]; ok {
					target = t
					resolved = true
				}
			}
		}
		if !resolved {
			polySlot[slot] = true
			delete(monoSlot, slot)
			continue
		}
		if existing, seen := monoSlot[slot]; seen && existing != target {
			polySlot[slot] = true
			delete(monoSlot, slot)
			continue
		}
		monoSlot[slot] = target
	}
	if !hasFlowSource || len(monoSlot) == 0 {
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
