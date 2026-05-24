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
//
// pairEnvOffset is the byte offset of env_ptr within the
// closure pair. wasm uses an 8-byte pair {fn_idx:i32@0,
// env_ptr:i32@4} so passes 4; native (arm64 / x86-64) uses a
// 16-byte pair {fn_ptr:i64@0, env_ptr:i64@8} so passes 8. The
// loaded value width is `WidthPtr`, which resolves per-target
// (4 on wasm32, 8 on native).
func Defunctionalise(prog *Program, pairEnvOffset int32) {
	returns := analyseReturnTargets(prog)
	for _, fn := range prog.Funcs {
		defunctionaliseFunc(fn, returns, pairEnvOffset)
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
		// Zero-init store (Phase 1e pre-zeroes rc-tracked slots).
		// Not a closure writer; ignore so it doesn't poison the slot.
		if i > 0 && fn.Ops[i-1].Kind == OpConstI32 && fn.Ops[i-1].I32 == 0 {
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
		// The return value is pushed before the exit rc dec-sweep
		// (a run of `OpLoadLocal s; OpCallDirect __fern_rc_dec;
		// OpDrop` triples inserted just before OpReturn). Skip
		// those triples backwards to find the op that actually
		// produces the returned value.
		j := i - 1
		for j >= 2 && fn.Ops[j].Kind == OpDrop &&
			fn.Ops[j-1].Kind == OpCallDirect && fn.Ops[j-1].Str == "__fern_rc_dec" &&
			fn.Ops[j-2].Kind == OpLoadLocal {
			j -= 3
		}
		if j < 0 {
			return "", false
		}
		prev := fn.Ops[j]
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

func defunctionaliseFunc(fn *Func, returns map[string]string, pairEnvOffset int32) {
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
	// Phase 1 runs as a fixed-point loop so a chain like
	// `var a = MakeClosure(T); var b = a; b()` propagates the
	// monomorphic target from `a` through `b` (and any further
	// hops). Each iteration either tightens the analysis (new
	// monoSlot entries) or terminates. Without the loop, only
	// the directly-OpMakeClosure-preceded slot was caught —
	// closure values flowing through any intermediate variable
	// kept the OpCallIndirect path and crashed at the backend
	// (`call r11` on a closure-pair pointer, since the deref
	// to fn-pointer-from-pair only happens on the
	// OpCallClosureDirect rewrite).
	for {
		changed := false
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
					if prev.Str == "__fern_rc_inc" && i >= 2 && fn.Ops[i-2].Kind == OpLoadLocal {
						// rc-tracked alias `b = a` lowers to
						// `OpLoadLocal a; OpCallDirect __fern_rc_inc;
						// OpStoreLocal b`. rc_inc returns its
						// argument unchanged, so look through it to
						// the slot being aliased.
						src := fn.Ops[i-2].I32
						if t, ok := monoSlot[src]; ok {
							target = t
							resolved = true
						} else if polySlot[src] {
							polySlot[slot] = true
							delete(monoSlot, slot)
							changed = true
							continue
						}
					} else if t, ok := returns[prev.Str]; ok {
						target = t
						resolved = true
					}
				case OpLoadLocal:
					// Value flowing through another slot. If
					// the source slot is already known to be
					// monomorphic, propagate its target; if
					// it's poly, this slot also becomes poly;
					// if unknown yet, leave for the next
					// fixed-point iteration.
					if t, ok := monoSlot[prev.I32]; ok {
						target = t
						resolved = true
					} else if polySlot[prev.I32] {
						polySlot[slot] = true
						delete(monoSlot, slot)
						changed = true
						continue
					}
				case OpConstI32:
					if prev.I32 == 0 {
						// Zero-init store the rc dec-sweep relies on
						// (Phase 1e zeroes rc-tracked slots, closures
						// included, so the exit null-guard fires).
						// It is not a closure writer; skip it so it
						// doesn't poison the mono-slot analysis.
						continue
					}
				}
			}
			if !resolved {
				if _, already := polySlot[slot]; !already {
					polySlot[slot] = true
					delete(monoSlot, slot)
					changed = true
				}
				continue
			}
			if existing, seen := monoSlot[slot]; seen && existing != target {
				polySlot[slot] = true
				delete(monoSlot, slot)
				changed = true
				continue
			}
			if existing, seen := monoSlot[slot]; !seen || existing != target {
				monoSlot[slot] = target
				changed = true
			}
		}
		if !changed {
			break
		}
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
				out = append(out, Op{Kind: OpConstI32, I32: pairEnvOffset})
				out = append(out, Op{Kind: OpAdd})
				out = append(out, Op{Kind: OpLoad, Width: WidthPtr})
				out = append(out, Op{Kind: OpCallClosureDirect, Str: target, I32: origArgc + 1})
				i++ // skip the original OpCallIndirect
				continue
			}
		}
		out = append(out, op)
	}
	fn.Ops = out
}
