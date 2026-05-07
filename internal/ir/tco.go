// Tail-call optimisation as an IR transform.
//
// A self-recursive call in tail position — the AST shape `return f(args)`
// inside `function f(...)` — lowers to two adjacent ops:
//
//	OpCallDirect f argc=N
//	OpReturn  (or OpReturnVoid)
//
// TailCallOptimize rewrites every such pair into a parameter rebind plus
// a backward branch to a synthetic outer loop wrapping the function
// body. Recursion that would otherwise grow the call stack now reuses
// the current activation, so factorial / Fibonacci tests stay O(1) in
// stack depth across every IR-consuming backend.
//
// The pass is conservative — it only fires on direct calls to the
// enclosing function with a matching argument count. Mutual recursion
// would need a trampoline and isn't handled.
package ir

// TailCallOptimize rewrites every self-tail call in prog into a
// parameter-rebind + branch-to-loop-top, in place. Functions without
// any self-tail call are left alone (so simple programs don't pay
// for an extra loop-wrapper).
func TailCallOptimize(prog *Program) {
	for _, fn := range prog.Funcs {
		applyTCO(fn)
	}
}

// applyTCO transforms a single function. The shape of the rewrite:
//
// before:
//
//	... body with `OpCallDirect f argc=N; OpReturn` somewhere ...
//
// after:
//
//	OpLoop void
//	  ... body, with each tail-call pair rewritten as ...
//	  OpStoreLocal <slot of param N-1>
//	  ...
//	  OpStoreLocal <slot of param 0>
//	  OpBr <depth-back-to-the-wrapping-loop>
//	  ... rest of body (unchanged) ...
//	OpEnd
//
// Args were pushed left-to-right by the original call lowering, so the
// rightmost arg sits on top of the value stack. Rebinding pops them in
// reverse so each argument lands in the correct parameter slot.
//
// Branch depth is computed by walking the wrapped body with a depth
// counter: a tail call sitting inside K nested block/loop/if scopes
// (counting the wrapper) targets the wrapper as `br K-1` (because the
// wrapper is the outermost open scope, K-1 levels from innermost).
func applyTCO(fn *Func) {
	if !hasSelfTailCall(fn) {
		return
	}
	wrapped := make([]Op, 0, len(fn.Ops)+2)
	wrapped = append(wrapped, Op{Kind: OpLoop, I32: BlockTypeVoid})
	wrapped = append(wrapped, fn.Ops...)
	wrapped = append(wrapped, Op{Kind: OpEnd})

	out := make([]Op, 0, len(wrapped))
	depth := int32(0)
	for i := 0; i < len(wrapped); i++ {
		op := wrapped[i]
		// Track depth before considering the rewrite, so a tail call
		// sees the depth of the scope it currently sits inside.
		switch op.Kind {
		case OpBlock, OpLoop, OpIf:
			depth++
		case OpEnd:
			depth--
		}
		if isSelfTailCall(op, fn) && i+1 < len(wrapped) {
			next := wrapped[i+1]
			if next.Kind == OpReturn || next.Kind == OpReturnVoid {
				for p := int32(len(fn.Params)) - 1; p >= 0; p-- {
					out = append(out, Op{Kind: OpStoreLocal, I32: p, Pos: op.Pos})
				}
				out = append(out, Op{Kind: OpBr, I32: depth - 1, Pos: op.Pos})
				i++ // also consume the OpReturn
				continue
			}
		}
		out = append(out, op)
	}
	fn.Ops = out
}

// hasSelfTailCall reports whether fn contains at least one
// `OpCallDirect <self> argc=N; OpReturn` pair worth rewriting. The
// pre-walk lets applyTCO skip the wrap entirely when there's nothing
// to optimise, keeping non-recursive function bodies unchanged.
func hasSelfTailCall(fn *Func) bool {
	for i := 0; i < len(fn.Ops)-1; i++ {
		if !isSelfTailCall(fn.Ops[i], fn) {
			continue
		}
		next := fn.Ops[i+1]
		if next.Kind == OpReturn || next.Kind == OpReturnVoid {
			return true
		}
	}
	return false
}

func isSelfTailCall(op Op, fn *Func) bool {
	return op.Kind == OpCallDirect && op.Str == fn.Name && op.I32 == int32(len(fn.Params))
}
