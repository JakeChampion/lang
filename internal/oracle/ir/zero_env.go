package ir

// Zero-capture env-load folding.
//
// `OpConstFunc` materialises a `{fn_ptr, env_ptr}` pair whose env_ptr is 0 —
// it is a plain function value, there is nothing to capture. `Defunctionalise`
// nonetheless emits the generic fetch at every call site:
//
//	OpLoadLocal slot ; OpConstI32 pairEnvOffset ; OpAdd ; OpLoad
//	OpCallClosureDirect target
//
// Three of those four ops read memory to recover a constant. Inside a loop it
// is three per call per element, and on the fused `map.map.reduce` of #9731 —
// three calls an element — that fetch was the whole of what still separated it
// from a hand-written loop CALLING the same element functions
// (docs/ARRAY-FUSION-OPERATORS.md's `call_loop` control).
//
// `ElideClosurePair` removes the same sequence and more, but only for a slot
// whose EVERY reader is one of these call sites; a slot that is also dropped,
// or passed on, keeps the detour. This pass asks a narrower question with a
// simpler answer: is the value a plain function, whose env is 0 whatever else
// is done with it? That does not depend on the other readers at all.
//
// Which makes the reach of this pass smaller than it sounds. It is NOT "every
// loop calling a zero-capture closure" — `ElideClosurePair` has those already,
// and two microbenchmarks written to demonstrate the general case measured
// identically with this pass on and off. What lands here is a slot the earlier
// pass declined, and in this tree that is the fused-pipeline shape, whose
// closures are also dropped.
//
// Runs after InlineZeroCaptureClosures, which is what turns a zero-capture
// OpMakeClosure into the OpConstFunc this keys on.
func FoldZeroCaptureEnvLoads(prog *Program, pairEnvOffset int32) {
	for _, fn := range prog.Funcs {
		foldZeroCaptureEnvLoadsIn(fn, pairEnvOffset)
	}
}

func foldZeroCaptureEnvLoadsIn(fn *Func, pairEnvOffset int32) {
	// A slot qualifies when every write to it is an OpConstFunc. One write
	// from anywhere else and the value is not known to be a plain function.
	plain := map[int32]bool{}
	for i, op := range fn.Ops {
		if op.Kind != OpStoreLocal {
			continue
		}
		if i > 0 && fn.Ops[i-1].Kind == OpConstFunc {
			if _, seen := plain[op.I32]; !seen {
				plain[op.I32] = true
			}
			continue
		}
		plain[op.I32] = false
	}
	if len(plain) == 0 {
		return
	}

	out := make([]Op, 0, len(fn.Ops))
	for i := 0; i < len(fn.Ops); i++ {
		if i+4 < len(fn.Ops) &&
			fn.Ops[i].Kind == OpLoadLocal && plain[fn.Ops[i].I32] &&
			fn.Ops[i+1].Kind == OpConstI32 && fn.Ops[i+1].I32 == pairEnvOffset &&
			fn.Ops[i+2].Kind == OpAdd &&
			fn.Ops[i+3].Kind == OpLoad &&
			fn.Ops[i+4].Kind == OpCallClosureDirect {
			// The env of a plain function value is 0. Push it instead of
			// reading it back out of the static cell.
			out = append(out, Op{Kind: OpConstI32, I32: 0})
			i += 3 // the call is re-emitted by the loop's next iteration
			continue
		}
		out = append(out, fn.Ops[i])
	}
	fn.Ops = out
}
