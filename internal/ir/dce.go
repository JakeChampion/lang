// Dead-code elimination on the lowered IR.
//
// After Fold collapses constant control flow, op lists routinely
// contain stretches that are unreachable because some earlier op
// already exited the surrounding scope. The most common shape is a
// `return` followed by a synthetic implicit-return that the lowering
// pass tacked on at function end (in `lowerFunc`) — once the body
// always-returns, that trailing implicit return is dead and should
// be dropped so the emitted code stays compact.
//
// EliminateDeadCode walks each function's ops linearly, tracking
// scope depth, and drops every op that sits between a "definitely
// exits this scope" terminator and the next control-flow merge.
//
// Terminators:
//   - OpReturn / OpReturnVoid       — exit the function entirely
//   - OpBr                          — unconditional branch (forward
//                                     for blocks/ifs, backward for
//                                     loops); subsequent ops in the
//                                     same scope can't fall through
//
// `OpBrIf` is conditional, so it does NOT make subsequent code dead.
//
// Merges that "revive" reachability:
//   - OpEnd at the depth where the terminator sat — ends the scope
//     the dead region lived in
//   - OpElse at the same depth — the else-arm of an if starts fresh
//
// Nested scopes opened *after* a terminator are kept depth-tracked
// (so we count their OpEnds correctly) but their bodies are still
// dropped — the outer dead state stays in effect until we exit back
// to the depth of the terminator.

package ir

// EliminateDeadCode rewrites every function in prog to drop ops that
// can't be reached because an earlier op in the same scope already
// returned or unconditionally branched away.
//
// The pass is single-pass and idempotent: a second call reproduces
// the same op slice. Backends never need to re-run it.
func EliminateDeadCode(prog *Program) {
	for _, fn := range prog.Funcs {
		fn.Ops = dceOnce(fn.Ops)
	}
}

// dceOnce walks ops once and returns the live subset. Depth tracks
// the current scope nesting; dead toggles when we cross a terminator
// (entering a dead region) or a merge point (leaving one).
func dceOnce(ops []Op) []Op {
	out := make([]Op, 0, len(ops))
	depth := int32(0)
	dead := false
	deadDepth := int32(0)
	for _, op := range ops {
		switch op.Kind {
		case OpBlock, OpLoop, OpIf:
			// Opening a new scope inside a dead region — track its
			// depth so the matching OpEnd lines up, but keep the op
			// itself dropped along with the body.
			if !dead {
				out = append(out, op)
			}
			depth++
			continue
		case OpEnd:
			// Leaving a scope. If the dead region opened at this
			// scope (depth equals deadDepth at the time of the
			// terminator), the OpEnd marks the end of the dead
			// region — clear the flag and emit the OpEnd.
			if dead && depth == deadDepth {
				dead = false
			}
			if !dead {
				out = append(out, op)
			}
			depth--
			continue
		case OpElse:
			// Entering the else-arm of an if. If we were dead in the
			// then-arm of this same if, the else-arm starts fresh.
			if dead && depth == deadDepth {
				dead = false
			}
			if !dead {
				out = append(out, op)
			}
			continue
		}
		// Generic op: drop while dead, otherwise keep and check
		// whether it terminates the current scope.
		if dead {
			continue
		}
		out = append(out, op)
		if isTerminator(op.Kind) {
			dead = true
			deadDepth = depth
		}
	}
	return out
}

// isTerminator reports whether op unconditionally exits its enclosing
// scope. OpReturn / OpReturnVoid leave the function; OpBr leaves the
// scope at the supplied depth (and any inner scopes along the way).
// OpBrIf doesn't qualify because the branch is conditional — control
// can fall through.
func isTerminator(k OpKind) bool {
	switch k {
	case OpReturn, OpReturnVoid, OpBr:
		return true
	}
	return false
}
