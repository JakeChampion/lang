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
// Pattern recognised:
//
//   writers (per slot, either shape is OK):
//     root  : ... eval captures ; OpMakeClosure name caps=N ; OpStoreLocal slot
//     alias : OpLoadLocal X ; OpStoreLocal slot     ; copy from another eligible slot
//
//   readers (per slot, either shape is OK):
//     canonical: OpLoadLocal slot ; OpConstI32 pairEnvOffset ;
//                OpAdd ; OpLoad ; OpCallClosureDirect target argc=…
//     alias    : OpLoadLocal slot ; OpStoreLocal Y           ; copy into another eligible slot
//
// Rewritten as:
//
//   root writers: OpMakeClosure → OpMakeEnv (pushes env_ptr only,
//   no pair alloc).
//   canonical readers: the const+add+load triple is dropped; the
//   slot's value is consumed directly as env_ptr by the call.
//   alias writers/readers: unchanged in shape — they now pipe
//   env_ptr (a plain i32) through the slot chain instead of a
//   closure-pair pointer.
//
// Eligibility is computed by a fixed-point worklist: a slot
// fails if any non-root writer or non-canonical reader points
// at a failed slot, and the failure propagates through alias
// edges. A surviving slot's equivalence class is required to
// contain at least one root writer somewhere — otherwise the
// chain doesn't actually carry a closure-pair value.
//
// Pairs with ANY writer/reader outside these shapes (closure
// passed to another function, stored in a struct field, etc.)
// keep the original layout — defunctionalisation already fell
// back to OpCallIndirect for those.

package ir

import (
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

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
	// A callee whose result type is a FuncType hands back a closure PAIR, so
	// a local it initialises holds one even though no OpMakeClosure wrote the
	// slot. That is the third way a slot comes to hold a pair, and the drop
	// rewrite below needs it: `var add5 = makeAdder(5);` leaked its env on
	// every call because the slot's provenance was invisible here (#8622).
	returnsClosure := map[string]bool{}
	for _, fn := range prog.Funcs {
		if _, ok := fn.ReturnType.(*ast.FuncType); ok {
			returnsClosure[fn.Name] = true
		}
	}
	for _, fn := range prog.Funcs {
		elideClosurePairFunc(fn, pairEnvOffset, returnsClosure)
	}
}

func elideClosurePairFunc(fn *Func, pairEnvOffset int32, returnsClosure map[string]bool) {
	// Two writer shapes contribute to a slot's eligibility:
	//
	//   (a) "root": OpStoreLocal preceded by OpMakeClosure.
	//       Rewriting OpMakeClosure → OpMakeEnv drops the
	//       pair alloc.
	//   (b) "alias": OpStoreLocal preceded by OpLoadLocal X.
	//       The slot just copies X's value. The slot is
	//       eligible iff X is also eligible (mutual recursion).
	//
	// Two reader shapes contribute:
	//
	//   (c) "canonical": OpLoadLocal followed by
	//       [OpConstI32 pairEnvOffset, OpAdd, OpLoad,
	//        OpCallClosureDirect]. The +pairEnvOffset/+add/+load
	//       triple is dropped on rewrite; the call now consumes
	//       the slot's value directly as env_ptr.
	//   (d) "alias": OpLoadLocal followed by OpStoreLocal Y.
	//       Eligible iff Y is also eligible.
	//
	// Any other writer or reader shape disqualifies the slot.
	// A disqualified slot also disqualifies every slot reached
	// via an alias edge — a fixed-point worklist handles that
	// propagation.
	type writer struct {
		storeIdx       int
		makeClosureIdx int // -1 if not a root writer
		aliasSrc       int32
		aliasOk        bool // true when storeIdx is preceded by OpLoadLocal
		// callPair is true when the store is fed by a direct call whose
		// callee returns a function value: the slot holds a pair the
		// caller owns, even though nothing here built it.
		callPair bool
	}
	type reader struct {
		loadIdx       int
		canonicalOk   bool
		canonDropIdxs [3]int // the const+add+load indices to drop
		aliasDst      int32  // target slot when followed by OpStoreLocal
		aliasOk       bool
	}
	writers := map[int32][]writer{}
	readers := map[int32][]reader{}
	// Slots that fail any non-alias check up front. Aliases that
	// land on a failed slot are disqualified through the worklist.
	failed := map[int32]bool{}
	candidates := map[int32]bool{}

	noteCandidate := func(slot int32) {
		candidates[slot] = true
	}

	for i, op := range fn.Ops {
		switch op.Kind {
		case OpStoreLocal, OpTeeLocal:
			// OpTeeLocal would leave the closure-pair value on
			// the operand stack post-store, which can't match
			// either of the recognised reader shapes (canonical
			// dance needs an OpLoadLocal to start, alias needs
			// an OpStoreLocal to land on). Tee = disqualified.
			if op.Kind == OpTeeLocal {
				failed[op.I32] = true
				continue
			}
			w := writer{storeIdx: i, makeClosureIdx: -1}
			if i > 0 {
				switch prev := fn.Ops[i-1]; prev.Kind {
				case OpMakeClosure:
					w.makeClosureIdx = i - 1
				case OpLoadLocal:
					w.aliasOk = true
					w.aliasSrc = prev.I32
				case OpRcInc:
					if i >= 2 && fn.Ops[i-2].Kind == OpLoadLocal {
						// rc-tracked alias `b = a` → `OpLoadLocal a;
						// OpRcInc; OpStoreLocal b`. rc_inc passes its
						// argument through, so the alias source is
						// the slot loaded before it.
						w.aliasOk = true
						w.aliasSrc = fn.Ops[i-2].I32
					} else {
						failed[op.I32] = true
					}
				case OpCallDirect:
					// Still ineligible for ELISION — the slot holds a real
					// pair, so its readers cannot be collapsed. But the
					// drop rewrite below wants to know it IS a pair.
					w.callPair = returnsClosure[prev.Str]
					failed[op.I32] = true
				case OpConstI32:
					if prev.I32 == 0 {
						// Zero-init store (Phase 1e pre-zeroes
						// rc-tracked slots, closures included). Not a
						// closure-pair writer; skip it so it neither
						// contributes a root nor disqualifies the slot.
						continue
					}
					failed[op.I32] = true
				default:
					failed[op.I32] = true
				}
			} else {
				failed[op.I32] = true
			}
			writers[op.I32] = append(writers[op.I32], w)
			noteCandidate(op.I32)
		case OpLoadLocal:
			r := reader{loadIdx: i}
			// Canonical [const + add + load + call_closure_direct].
			if i+4 < len(fn.Ops) {
				o1 := fn.Ops[i+1]
				o2 := fn.Ops[i+2]
				o3 := fn.Ops[i+3]
				o4 := fn.Ops[i+4]
				if o1.Kind == OpConstI32 && o1.I32 == pairEnvOffset &&
					o2.Kind == OpAdd &&
					o3.Kind == OpLoad &&
					o4.Kind == OpCallClosureDirect {
					r.canonicalOk = true
					r.canonDropIdxs = [3]int{i + 1, i + 2, i + 3}
				}
			}
			// Alias: OpLoadLocal followed by OpStoreLocal.
			if !r.canonicalOk && i+1 < len(fn.Ops) && fn.Ops[i+1].Kind == OpStoreLocal {
				r.aliasOk = true
				r.aliasDst = fn.Ops[i+1].I32
			}
			// rc-tracked alias read: OpLoadLocal slot; OpRcInc;
			// OpStoreLocal dst. The dst slot copies this value
			// (through the pass-through rc_inc), so it's an alias
			// edge just like the bare load+store form.
			if !r.canonicalOk && !r.aliasOk && i+2 < len(fn.Ops) &&
				fn.Ops[i+1].Kind == OpRcInc &&
				fn.Ops[i+2].Kind == OpStoreLocal {
				r.aliasOk = true
				r.aliasDst = fn.Ops[i+2].I32
			}
			// Benign exit dec: OpLoadLocal slot; OpRcDec. The dec
			// sweep reads every tracked slot at function exit;
			// rc_dec consumes the pointer and its result is
			// dropped, so the value never escapes. Skip it so it
			// neither qualifies nor disqualifies the slot.
			if !r.canonicalOk && !r.aliasOk && i+1 < len(fn.Ops) &&
				fn.Ops[i+1].Kind == OpRcDec {
				continue
			}
			// Benign closure drop: OpLoadLocal slot; OpCallDirect to
			// the generic __fern_closure_drop OR a per-closure
			// __closure_drop_<name> thunk (Stage 3). Same as the
			// rc_dec exit-sweep skip — the handler consumes the
			// pointer and its result is dropped, so the value doesn't
			// escape and the slot stays elision-eligible. Crucially,
			// keeping the slot elision-eligible turns the closure into
			// a BARE ENV (no pair), which is exactly what the thunk
			// assumes when it reads captures at [env+offset].
			if !r.canonicalOk && !r.aliasOk && i+1 < len(fn.Ops) &&
				fn.Ops[i+1].Kind == OpCallDirect &&
				(fn.Ops[i+1].Str == "__fern_closure_drop" ||
					strings.HasPrefix(fn.Ops[i+1].Str, "__closure_drop_")) {
				continue
			}
			if !r.canonicalOk && !r.aliasOk {
				failed[op.I32] = true
			}
			readers[op.I32] = append(readers[op.I32], r)
			noteCandidate(op.I32)
		}
	}

	// Fixed-point: a slot fails if any of its writers (other than
	// roots) point to a failed source, or any of its readers
	// (other than canonical) point to a failed destination.
	for {
		changed := false
		for slot := range candidates {
			if failed[slot] {
				continue
			}
			for _, w := range writers[slot] {
				if w.makeClosureIdx >= 0 {
					continue // root
				}
				if !w.aliasOk || failed[w.aliasSrc] {
					failed[slot] = true
					changed = true
					break
				}
			}
			if failed[slot] {
				continue
			}
			for _, r := range readers[slot] {
				if r.canonicalOk {
					continue
				}
				if !r.aliasOk || failed[r.aliasDst] {
					failed[slot] = true
					changed = true
					break
				}
			}
		}
		if !changed {
			break
		}
	}

	// Also require that every slot has at least one root writer
	// SOMEWHERE in its equivalence class — otherwise the chain
	// terminates without an OpMakeClosure and isn't actually a
	// closure-pair flow. Build the union-find rooted at root
	// writers.
	hasRootInChain := map[int32]bool{}
	for slot := range candidates {
		if failed[slot] {
			continue
		}
		// DFS the alias-source graph; any root writer found
		// makes this slot (and everything reachable via aliases)
		// validate.
		stack := []int32{slot}
		visited := map[int32]bool{slot: true}
		found := false
		for len(stack) > 0 {
			s := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, w := range writers[s] {
				if w.makeClosureIdx >= 0 {
					found = true
				}
				if w.aliasOk && !visited[w.aliasSrc] {
					visited[w.aliasSrc] = true
					stack = append(stack, w.aliasSrc)
				}
			}
		}
		if found {
			hasRootInChain[slot] = true
		}
	}

	// Apply rewrites. For each surviving slot:
	//   - every OpMakeClosure root writer becomes OpMakeEnv.
	//   - every canonical reader has its [const, add, load]
	//     triple dropped. The trailing OpCallClosureDirect now
	//     consumes the loaded slot value directly as env_ptr.
	//   - alias writers/readers keep their shape — they now
	//     pipe env_ptr (a plain i32) through the slot chain.
	dropped := map[int]bool{}
	mcToEnv := map[int]bool{}
	elidedSlot := map[int32]bool{}
	for slot := range candidates {
		if failed[slot] || !hasRootInChain[slot] {
			continue
		}
		elidedSlot[slot] = true
		for _, w := range writers[slot] {
			if w.makeClosureIdx >= 0 {
				mcToEnv[w.makeClosureIdx] = true
			}
		}
		for _, r := range readers[slot] {
			if r.canonicalOk {
				dropped[r.canonDropIdxs[0]] = true
				dropped[r.canonDropIdxs[1]] = true
				dropped[r.canonDropIdxs[2]] = true
			}
		}
	}

	// A closure slot that did NOT elide — one passed to a function, the
	// ordinary callback — still holds a {fn, env, drop_fn, env} PAIR, and
	// both drops emitDec can have chosen for it release only part of that.
	// A per-closure __closure_drop_<name> thunk reads captures at
	// [env+offset], so it is only correct on a BARE ENV; the generic
	// __fern_closure_drop frees the block it is handed, which on a pair is
	// the pair and never the env behind it.
	//
	// __drop_closure_value is the one that fits a pair: it is_unique-gates,
	// dispatches through the drop-fn pointer the pair carries at 2*ptrW
	// (that pointer IS the thunk, reached with the env rather than the
	// pair), then frees the pair block. Route BOTH drops there.
	//
	// The generic arm matters on its own: emitDec picks it whenever the
	// closure has no rc-tracked capture, so a lambda capturing one scalar
	// by value leaked its whole env box on every call that took it as an
	// argument (#8546) even once the thunk arm was routed (#8545).
	// __drop_closure_value is safe for the targets that arm also covers —
	// a static OpConstFunc cell carries the immortal rc sentinel, so the
	// is_unique gate skips it, and a zero-capture closure's drop-fn slot is
	// 0, which the inner guard skips.
	// pairSlot names the slots that provably hold a {fn, env, drop_fn, env}
	// pair: an OpMakeClosure wrote them directly. The generic arm below is
	// gated on it because `__fern_closure_drop` is NOT closure-specific —
	// genArrOfArrDropFn reuses it as the per-element drop for an
	// array-of-arrays, and several generated helpers call it on an env they
	// have already dispatched through. Rewriting by helper NAME alone
	// therefore reached values that are not pairs: it recursed the
	// pair-dropper into itself (every closure program segfaulted) and
	// dispatched through a non-pair's word 2 (`slice_views`, which contains
	// no closure at all, trapped with `indirect call type mismatch`). A
	// slot reached only through an alias has makeClosureIdx -1 and is left
	// alone, which loses a release rather than inventing one.
	pairSlot := map[int32]bool{}
	for slot, ws := range writers {
		for _, w := range ws {
			if w.makeClosureIdx >= 0 || w.callPair {
				pairSlot[slot] = true
			}
		}
	}
	// An alias of a pair is a pair. `var g = f;` writes the slot through
	// OpLoadLocal (or OpRcInc over one), which carries no OpMakeClosure of
	// its own, so without this the drop on the alias is left alone — a
	// missed release rather than a wrong one, but it is most of the shapes
	// the corpus holds. Fixpoint because an alias can name an alias.
	for changed := true; changed; {
		changed = false
		for slot, ws := range writers {
			if pairSlot[slot] {
				continue
			}
			for _, w := range ws {
				if w.aliasOk && pairSlot[w.aliasSrc] {
					pairSlot[slot] = true
					changed = true
					break
				}
			}
		}
	}
	for i := 0; i+1 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind != OpLoadLocal || elidedSlot[fn.Ops[i].I32] {
			continue
		}
		n := fn.Ops[i+1]
		if n.Kind != OpCallDirect {
			continue
		}
		thunk := strings.HasPrefix(n.Str, "__closure_drop_")
		generic := n.Str == "__fern_closure_drop" && pairSlot[fn.Ops[i].I32]
		if thunk || generic {
			fn.Ops[i+1].Str = "__drop_closure_value"
			fn.Ops[i+1].Runtime = false
			fn.Ops[i+1].Width = 0
		}
	}

	if len(mcToEnv) == 0 && len(dropped) == 0 {
		return
	}
	for idx := range mcToEnv {
		fn.Ops[idx].Kind = OpMakeEnv
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
