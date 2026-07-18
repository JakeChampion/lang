// Function inlining at the IR level.
//
// Replaces every OpCallDirect to a "small" callee with a copy of
// the callee's op list, with arguments bound to fresh local slots
// in the caller. Runs after Lower / before Fold / DCE so the
// constants exposed by a substituted body get folded in the same
// pipeline.
//
// Eligibility — deliberately conservative, but extended past the
// strictly-leaf shape so stdlib helpers with simple control flow
// or sub-calls still inline:
//
//   - body length ≤ inlineSizeLimit (skip blow-up cases).
//   - no OpCallIndirect / OpMakeClosure: closures bring an extra
//     layer of dispatch + a `__call_scratch` requirement; not
//     worth the per-callee book-keeping yet.
//   - no recursive call to the caller itself (inlineOps detects
//     this by name match against the enclosing function).
//
// What gets rewritten at a call site:
//
//   <args pushed to stack by previous ops>
//   OpCallDirect <callee> argc=P
//
// becomes
//
//   <args pushed by previous ops>            (unchanged)
//   OpStoreLocal <fresh slot for param P-1>  (rightmost arg → rightmost param)
//   …
//   OpStoreLocal <fresh slot for param 0>
//   OpBlock <result-type>                    (return-target wrapper)
//   <callee's body ops, with every OpLoadLocal / OpStoreLocal index
//    rewritten to add the offset of the fresh slot range AND every
//    OpReturn / OpReturnVoid translated to a br targeting the
//    wrapper>
//   OpEnd                                    (closes wrapper)
//
// The wrapper block lets early returns fall through to the
// inlined-body's continuation by branching out — same shape an
// optimising C compiler uses for inlined returns. Falls through to
// the wrapper end when the trailing terminator runs.
//
// The caller's ScratchTypes grows by the callee's full slot list
// (params + locals + scratches) — each entry's type carries
// through so codegen declares the right shape (i32 vs f32 vs
// i64 vs f64) for each WAT local. Each inline site gets its own
// slot range; the simple version doesn't try to coalesce ranges
// across consecutive inlines.

package ir

import (
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// inlineSizeLimit caps how many ops a callee can have to remain
// eligible. Tuned to allow the bulk of stdlib helpers (e.g.
// __substr_eq, __map_hash, the small hex / b64 char-classifiers)
// to inline through their internal control flow. Tweak as real
// workloads emerge.
const inlineSizeLimit = 80

// Inline rewrites every OpCallDirect to an eligible callee in prog
// as the callee's op list with parameters bound to fresh local
// slots. The pass is conservative — programs without inlineable
// callees are unchanged in O(N) walk time.
//
// Iterates up to inlineMaxPasses times: a single pass substitutes
// the OUTER callee (e.g. inlining `s.contains(...)` into main),
// but the inlined body may itself contain calls to other eligible
// callees (`__substr_eq` from inside `contains`). Without a
// second pass, those inner calls survive — the stdlib is full of
// "small helper calls another small helper" chains. Cap the
// iteration count to bound code growth on the worst case.
//
// Order in the production pipeline: Lower → Inline → Fold → DCE →
// emit. Inlining first exposes constant
// arithmetic in substituted bodies (e.g. `dbl(7)` becomes `7 * 2`)
// for Fold to collapse, then DCE drops anything unreachable that
// surfaces.
func Inline(prog *Program) {
	for i := 0; i < inlineMaxPasses; i++ {
		candidates := findInlineCandidates(prog)
		if len(candidates) == 0 {
			return
		}
		changed := false
		for _, fn := range prog.Funcs {
			before := len(fn.Ops)
			fn.Ops = inlineOps(fn, fn.Ops, candidates)
			if len(fn.Ops) != before {
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

// inlineMaxPasses caps the iteration depth. Three passes covers
// the deepest call chain the migrated stdlib builds today
// (`Map.set` → `__map_grow` / `__map_hash` → no further inlineable
// callees). Bumping further has diminishing returns and risks
// runaway code growth on pathological inputs.
const inlineMaxPasses = 3

// inlineCandidate is an eligible callee snapshot taken before any
// rewriting begins. Storing the body slice keeps the inliner from
// observing rewrites done to the callee itself when it appears
// later in the prog.Funcs list.
type inlineCandidate struct {
	fn   *Func
	body []Op // body up to and including the trailing OpReturn / OpReturnVoid
	// slotTypes lists the type of every slot the callee uses, in
	// slot order: params first, then user locals, then scratches.
	// Inlining appends these to the caller's ScratchTypes so
	// codegen declares each new local with the right WAT type.
	slotTypes []ast.Type
	// returnBlockType describes the wrapper block's result type
	// matched against the callee's return type. Void functions
	// open a void wrapper and use plain `br`; value-returning
	// functions open a typed wrapper so the br carries the
	// returned value through.
	returnBlockType int32
}

// findInlineCandidates scans every function in prog and returns
// the subset that meets the eligibility rules. The map's key is
// the function name so Inline can resolve OpCallDirect.Str
// directly.
func findInlineCandidates(prog *Program) map[string]inlineCandidate {
	out := map[string]inlineCandidate{}
	ptrW := prog.PtrW
	if ptrW == 0 {
		ptrW = 4
	}
	for _, fn := range prog.Funcs {
		if !isInlineable(fn) {
			continue
		}
		slots := make([]ast.Type, 0, len(fn.Params)+len(fn.Locals)+len(fn.ScratchTypes))
		for _, p := range fn.Params {
			slots = append(slots, p.Type)
		}
		for _, v := range fn.Locals {
			slots = append(slots, v.Type)
		}
		slots = append(slots, fn.ScratchTypes...)
		// Two-word inline `dyn Trait` slot (wasm, ptrW==4): a dyn local is one
		// IR slot but a two-value `[data, vtable]` operand, and its exit-sweep
		// reclaim loads that pair and hands it to __drop_dyn_<set> as two args.
		// The splice mis-orders that pair against the slot's own store — the
		// reclaim runs on the slot's *pre-init* value and the fresh box is
		// stored un-swept, corrupting the freelist (a struct/enum-behind-dyn
		// churn traps: memory fault at 0xffffffff — #4786). Two-word STRINGS
		// inline fine (their reclaim is a single-arg __fern_str_dec, no split
		// pair), so this is dyn-specific. Boxed natives (ptrW==8) hold the dyn
		// value in one word and are unaffected. Skip such callees — the
		// closure-drop-thunk exclusion above is the same "don't inline this
		// shape" precedent.
		if ptrW == 4 && slotsHaveDynTrait(slots) {
			continue
		}
		out[fn.Name] = inlineCandidate{
			fn:              fn,
			body:            fn.Ops,
			slotTypes:       slots,
			returnBlockType: returnBlockTypeFor(fn.ReturnType, ptrW),
		}
	}
	return out
}

// slotsHaveDynTrait reports whether any slot type is a `dyn Trait` value —
// the two-word inline representation on wasm the inliner mis-splices (#4786).
func slotsHaveDynTrait(slots []ast.Type) bool {
	for _, t := range slots {
		if _, ok := t.(ast.DynTraitType); ok {
			return true
		}
	}
	return false
}

// isInlineable reports whether fn meets every eligibility rule.
// Internal control flow (block / loop / if / br / brif) and direct
// calls to other functions are allowed; OpCallIndirect /
// OpMakeClosure / oversized bodies disqualify.
func isInlineable(fn *Func) bool {
	// Source-level hints (#4412 Rec §14): @noinline is absolute;
	// @inline lifts only the SIZE cap — every shape-safety exclusion
	// below (closure drop thunks, indirect calls / closure makes, the
	// wasm dyn-slot case in findInlineCandidates) still applies, since
	// those guard correctness or unimplemented splice mechanics, not
	// cost.
	if fn.InlineHint == ast.InlineHintNever {
		return false
	}
	if len(fn.Ops) == 0 {
		return false
	}
	if fn.InlineHint != ast.InlineHintAlways && len(fn.Ops) > inlineSizeLimit {
		return false
	}
	// Never inline a per-closure drop thunk: it reads captures at
	// [env+offset], and ElideClosurePair only recognises the closure
	// drop as a benign reader (so the closure can become a bare env)
	// when the thunk is a single OpCallDirect. Inlining it splices
	// raw [slot+off] capture loads that disqualify elision, leaving a
	// {fn,env} PAIR the inlined thunk then misreads as an env.
	if strings.HasPrefix(fn.Name, "__closure_drop_") {
		return false
	}
	last := fn.Ops[len(fn.Ops)-1].Kind
	if last != OpReturn && last != OpReturnVoid && last != OpReturnPair {
		return false
	}
	for _, op := range fn.Ops {
		switch op.Kind {
		case OpCallIndirect, OpMakeClosure:
			return false
		}
	}
	return true
}

// inlineOps walks ops linearly and substitutes every OpCallDirect
// (and OpCallClosureDirect — defunctionalisation produces those
// at known target names too) to a known candidate. Calls to
// non-candidate functions are left untouched. The fn argument is
// the caller, mutated in place: each substitution appends the
// callee's slot types to fn.ScratchTypes.
func inlineOps(fn *Func, ops []Op, candidates map[string]inlineCandidate) []Op {
	out := make([]Op, 0, len(ops))
	for _, op := range ops {
		if op.Kind != OpCallDirect && op.Kind != OpCallClosureDirect {
			out = append(out, op)
			continue
		}
		cand, ok := candidates[op.Str]
		if !ok || cand.fn == fn {
			// Unknown callee or self-recursion — leave the call.
			out = append(out, op)
			continue
		}
		out = append(out, expandInline(fn, cand)...)
	}
	return out
}

// expandInline produces the op slice that replaces a single
// OpCallDirect site. Argument bindings, the return-target wrapper,
// and the body splice with renumbered slots + return-translation
// all happen here.
func expandInline(caller *Func, cand inlineCandidate) []Op {
	base := int32(len(caller.Params)) + int32(len(caller.Locals)) + int32(len(caller.ScratchTypes))
	caller.ScratchTypes = append(caller.ScratchTypes, cand.slotTypes...)

	// Bind arguments. Caller pushed args left-to-right, so the
	// rightmost argument is on top of the operand stack —
	// popping into reverse-order param slots lands each value in
	// the right place.
	out := make([]Op, 0, len(cand.body)+len(cand.slotTypes)+2)
	for p := int32(len(cand.fn.Params)) - 1; p >= 0; p-- {
		out = append(out, Op{Kind: OpStoreLocal, I32: base + p})
	}

	// Skip the wrapper block on the easy case: bodies with no
	// mid-body OpReturn / OpReturnVoid don't need an early-exit
	// target. Keeps the linear-leaf path bit-identical to the
	// pre-control-flow inliner so downstream peephole +
	// tee-folding still see the un-blocked load / store / op
	// stream they were tuned for.
	wrap := needsReturnWrapper(cand.body)
	if wrap {
		out = append(out, Op{Kind: OpBlock, I32: cand.returnBlockType})
	}

	// Splice the callee body with slot indices rebased onto the
	// caller's fresh range, control-flow depth tracked so each
	// Return translates to a br with the correct relative depth,
	// and the trailing terminator dropped (control falls through
	// to the wrapper's End — or to the caller continuation
	// directly when there's no wrapper).
	depth := int32(0)
	for i, op := range cand.body {
		isTrailing := i == len(cand.body)-1 &&
			(op.Kind == OpReturn || op.Kind == OpReturnVoid || op.Kind == OpReturnPair)
		if isTrailing {
			break
		}
		switch op.Kind {
		case OpBlock, OpLoop, OpIf:
			out = append(out, op)
			depth++
		case OpEnd:
			out = append(out, op)
			depth--
		case OpLoadLocal, OpStoreLocal, OpTeeLocal:
			op.I32 += base
			out = append(out, op)
		case OpReturn, OpReturnVoid, OpReturnPair:
			// Translate to a branch out of the wrapper block.
			// `depth` is the relative distance from this op to
			// the wrapper. The OpReturn's value (if any) is
			// already on the operand stack — wasm's br
			// semantics carry it through to the wrapper's
			// continuation.
			out = append(out, Op{Kind: OpBr, I32: depth})
		default:
			out = append(out, op)
		}
	}

	if wrap {
		out = append(out, Op{Kind: OpEnd})
	}
	return out
}

// needsReturnWrapper reports whether body contains any OpReturn /
// OpReturnVoid before its trailing terminator. Mid-body returns
// require the wrapper; pure straight-line + nested control flow
// without internal returns can splice cleanly with the trailing
// terminator just dropped.
func needsReturnWrapper(body []Op) bool {
	if len(body) == 0 {
		return false
	}
	for i, op := range body[:len(body)-1] {
		_ = i
		if op.Kind == OpReturn || op.Kind == OpReturnVoid || op.Kind == OpReturnPair {
			return true
		}
	}
	return false
}
