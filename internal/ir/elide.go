// Closure-pair allocation elision.
//
// Defunctionalisation rewrites monomorphic-flow OpCallIndirect
// sites to OpCallClosureDirect — but the closure-pair {fn_idx,
// env_ptr} the call site loaded its env from is still being
// allocated by OpMakeClosure. After defunctionalisation, the
// fn_idx field is dead: nothing reads it. This pass detects the
// "every reader of the slot was rewritten to load env_ptr +
// dispatch directly" shape and drops the pair allocation
// entirely. The slot now holds env_ptr directly; call sites
// load the slot value straight onto the stack instead of going
// through the +4-load detour.
//
// Pattern recognised (per slot):
//
//   writers (must all match):
//     ... eval captures ...
//     OpMakeClosure name caps=N
//     OpStoreLocal slot
//
//   readers (every one of them must match):
//     OpLoadLocal slot
//     OpConstI32 4
//     OpAdd
//     OpLoad                            ; env_ptr
//     OpCallClosureDirect target argc=…
//
// Rewritten as:
//
//   writers:
//     ... eval captures ...
//     OpMakeEnv name caps=N             ; pushes env_ptr only
//     OpStoreLocal slot
//
//   readers:
//     OpLoadLocal slot                  ; pushes env_ptr
//     OpCallClosureDirect target argc=… ; (the const-add-load
//                                         steps are gone)
//
// The slot now stores env_ptr (or 0 when no captures) directly.
// Pairs that have ANY reader outside this pattern (closure
// passed to another function, closure stored in a struct field,
// etc.) keep the original layout — defunctionalisation already
// fell back to OpCallIndirect for those, so they're not on this
// pass's radar anyway.

package ir

// ElideClosurePair drops dead closure-pair allocations in each
// function. Programs without OpMakeClosure are unchanged in O(N)
// walk time. Designed to run after Defunctionalise (which
// produces the OpCallClosureDirect call sites this pass keys
// off) and before the second Inline pass / Fold / DCE so the
// streamlined load-then-call sequence flows through the rest
// of the optimiser without the +ptrW detour in the way.
//
// pairEnvOffset must match what was passed to Defunctionalise
// (4 on wasm, 8 on native) — that's the OpConstI32 value this
// pass keys off when recognising the reader pattern.
func ElideClosurePair(prog *Program, pairEnvOffset int32) {
	for _, fn := range prog.Funcs {
		elideClosurePairFunc(fn, pairEnvOffset)
	}
}

func elideClosurePairFunc(fn *Func, pairEnvOffset int32) {
	type writer struct {
		storeIdx       int
		makeClosureIdx int
	}
	writers := map[int32][]writer{}
	readers := map[int32][]int{}

	for i, op := range fn.Ops {
		if op.Kind == OpStoreLocal || op.Kind == OpTeeLocal {
			mc := -1
			if i > 0 && fn.Ops[i-1].Kind == OpMakeClosure {
				mc = i - 1
			}
			writers[op.I32] = append(writers[op.I32], writer{i, mc})
			continue
		}
		if op.Kind == OpLoadLocal {
			readers[op.I32] = append(readers[op.I32], i)
		}
	}

	// Eligible slots:
	//   - every writer's preceding op is OpMakeClosure
	//   - every reader is followed by the
	//     [const 4, add, load, call_closure_direct] pattern
	//
	// Track the indices to drop per eligible reader; we batch
	// the removal into a single rebuild at the end.
	type elide struct {
		writerMakeClosureIdxs []int
		dropIdxs              []int
	}
	plans := map[int32]*elide{}

	for slot, ws := range writers {
		ok := true
		mcIdxs := make([]int, 0, len(ws))
		for _, w := range ws {
			if w.makeClosureIdx < 0 {
				ok = false
				break
			}
			mcIdxs = append(mcIdxs, w.makeClosureIdx)
		}
		if !ok {
			continue
		}
		dropIdxs := []int(nil)
		for _, loadIdx := range readers[slot] {
			if loadIdx+4 >= len(fn.Ops) {
				ok = false
				break
			}
			o1 := fn.Ops[loadIdx+1]
			o2 := fn.Ops[loadIdx+2]
			o3 := fn.Ops[loadIdx+3]
			o4 := fn.Ops[loadIdx+4]
			if o1.Kind != OpConstI32 || o1.I32 != pairEnvOffset ||
				o2.Kind != OpAdd ||
				o3.Kind != OpLoad ||
				o4.Kind != OpCallClosureDirect {
				ok = false
				break
			}
			dropIdxs = append(dropIdxs, loadIdx+1, loadIdx+2, loadIdx+3)
		}
		if !ok {
			continue
		}
		plans[slot] = &elide{writerMakeClosureIdxs: mcIdxs, dropIdxs: dropIdxs}
	}

	if len(plans) == 0 {
		return
	}

	// Apply the plans. First rewrite OpMakeClosure → OpMakeEnv
	// in place at the recorded indices; then build a new ops
	// slice skipping the dropped indices.
	dropped := map[int]bool{}
	for _, p := range plans {
		for _, mcIdx := range p.writerMakeClosureIdxs {
			fn.Ops[mcIdx].Kind = OpMakeEnv
		}
		for _, di := range p.dropIdxs {
			dropped[di] = true
		}
	}

	out := make([]Op, 0, len(fn.Ops)-len(dropped))
	for i, op := range fn.Ops {
		if dropped[i] {
			continue
		}
		out = append(out, op)
	}
	fn.Ops = out
}
