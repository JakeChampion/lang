// Function inlining at the IR level.
//
// Replaces every OpCallDirect to a "small" leaf function with a copy
// of the callee's op list, with arguments bound to fresh local slots
// in the caller. The pass runs after Lower / before Fold / DCE so
// the constants exposed by a substituted body get folded and any
// unreachable cleanup gets dropped in the same pipeline.
//
// Eligibility (deliberately conservative — match the AST inliner's
// restrictions where they still apply, drop the ones the IR's slot
// model makes safe):
//
//   - the callee's body is a linear op sequence ending in OpReturn
//     (or OpReturnVoid for void functions). No early returns, no
//     internal block / loop / if scopes, no break / continue.
//   - no call-emitting op anywhere in the body — direct calls,
//     indirect calls, allocation, string-runtime helpers, closure
//     construction. This forbids recursion implicitly and keeps
//     inlining from duplicating side effects.
//   - body length ≤ inlineSizeLimit, so we don't bloat the caller.
//
// Notably absent vs. the AST inliner: the "args must be simple"
// constraint. The IR substitution binds each argument once into a
// fresh local before the body runs, so even arg expressions with
// side effects evaluate exactly once.
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
//   <callee's body ops, with every OpLoadLocal / OpStoreLocal index
//    rewritten to add the offset of the fresh slot range>
//   <trailing OpReturn / OpReturnVoid dropped>
//
// The caller's ScratchTypes grows by the callee's full slot list
// (params + locals + scratches) — each entry's type carries
// through so codegen declares the right shape (i32 vs f32) for
// each WAT local. Each inline site gets its own slot range; the
// simple version doesn't try to coalesce ranges across
// consecutive inlines.

package ir

import "github.com/jakechampion/lang/internal/ast"

// inlineSizeLimit caps how many ops a callee can have to remain
// eligible. Tuned to allow simple arithmetic / accessor wrappers
// without letting larger helpers explode caller size. Tweak as
// real workloads emerge.
const inlineSizeLimit = 30

// Inline rewrites every OpCallDirect to an eligible callee in prog as
// the callee's op list with parameters bound to fresh local slots.
// The pass is conservative — programs without inlineable callees are
// unchanged in O(N) walk time.
//
// Order in the production pipeline: Lower → Inline → Fold → DCE →
// (TCO if arm32) → emit. Inlining first exposes constant arithmetic
// in substituted bodies (e.g. `dbl(7)` becomes `7 * 2`) for Fold to
// collapse, then DCE drops anything unreachable that surfaces.
func Inline(prog *Program) {
	candidates := findInlineCandidates(prog)
	if len(candidates) == 0 {
		return
	}
	for _, fn := range prog.Funcs {
		fn.Ops = inlineOps(fn, fn.Ops, candidates)
	}
}

// inlineCandidate is an eligible callee snapshot taken before any
// rewriting begins. Storing the body slice keeps the inliner from
// observing rewrites done to the callee itself when it appears later
// in the prog.Funcs list.
type inlineCandidate struct {
	fn   *Func
	body []Op // body up to and including the trailing OpReturn / OpReturnVoid
	// slotTypes lists the type of every slot the callee uses, in
	// slot order: params first, then user locals, then scratches.
	// Inlining appends these to the caller's ScratchTypes so codegen
	// declares each new local with the right WAT type.
	slotTypes []ast.Type
}

// findInlineCandidates scans every function in prog and returns the
// subset that meets the eligibility rules. The map's key is the
// function name so Inline can resolve OpCallDirect.Str directly.
func findInlineCandidates(prog *Program) map[string]inlineCandidate {
	out := map[string]inlineCandidate{}
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
		out[fn.Name] = inlineCandidate{
			fn:        fn,
			body:      fn.Ops,
			slotTypes: slots,
		}
	}
	return out
}

// isInlineable reports whether fn meets every eligibility rule.
// Returns false for functions that recurse, contain calls of any
// kind, use control flow, or exceed the size threshold.
func isInlineable(fn *Func) bool {
	if len(fn.Ops) == 0 || len(fn.Ops) > inlineSizeLimit {
		return false
	}
	last := fn.Ops[len(fn.Ops)-1].Kind
	if last != OpReturn && last != OpReturnVoid {
		return false
	}
	// Walk the body excluding the trailing terminator. Any other
	// terminator, control-flow op, or call-emitting op disqualifies
	// the function.
	for _, op := range fn.Ops[:len(fn.Ops)-1] {
		switch op.Kind {
		case OpBlock, OpLoop, OpIf, OpElse, OpEnd, OpBr, OpBrIf,
			OpReturn, OpReturnVoid,
			OpCallDirect, OpCallIndirect,
			OpAlloc, OpStrConcat, OpStrEq, OpMakeClosure:
			return false
		}
	}
	return true
}

// inlineOps walks ops linearly and substitutes every OpCallDirect to
// a known candidate. Calls to non-candidate functions are left
// untouched. The fn argument is the caller, mutated in place: each
// substitution appends the callee's slot types to fn.ScratchTypes.
func inlineOps(fn *Func, ops []Op, candidates map[string]inlineCandidate) []Op {
	out := make([]Op, 0, len(ops))
	for _, op := range ops {
		if op.Kind != OpCallDirect {
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
// OpCallDirect site. It allocates the fresh slot range, emits the
// arg-binding stores in reverse order (rightmost arg sits on top of
// the operand stack), splices the callee body with rewritten slot
// indices, and drops the trailing terminator so control falls into
// the caller's continuation.
func expandInline(caller *Func, cand inlineCandidate) []Op {
	base := int32(len(caller.Params)) + int32(len(caller.Locals)) + int32(len(caller.ScratchTypes))
	caller.ScratchTypes = append(caller.ScratchTypes, cand.slotTypes...)

	// Bind arguments. Caller pushed args left-to-right, so the
	// rightmost argument is on top of the operand stack — popping
	// into reverse-order param slots lands each value in the right
	// place.
	out := make([]Op, 0, len(cand.body)+len(cand.slotTypes))
	for p := int32(len(cand.fn.Params)) - 1; p >= 0; p-- {
		out = append(out, Op{Kind: OpStoreLocal, I32: base + p})
	}

	// Splice the callee body with slot indices rebased onto the
	// caller's fresh range. The trailing OpReturn / OpReturnVoid is
	// skipped — the value (if any) is already on the operand stack
	// where the caller's continuation expects it.
	for i, op := range cand.body {
		if i == len(cand.body)-1 && (op.Kind == OpReturn || op.Kind == OpReturnVoid) {
			continue
		}
		switch op.Kind {
		case OpLoadLocal, OpStoreLocal:
			op.I32 += base
		}
		out = append(out, op)
	}
	return out
}
