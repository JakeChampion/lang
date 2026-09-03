// Cleanup driver — runs PropagateCopies, ConstPropagate, and Fold to
// a fixed point. Each pass exposes opportunities for the others:
//
//   - ConstPropagate inlines a tracked constant into a load. The
//     load's parent op (often a binop) now has all-constant
//     operands, which Fold collapses.
//   - Fold collapsing an expression can leave a slot's only reader
//     gone, turning a previously-live tee into a dead tee that
//     PropagateCopies drops.
//   - Dropping a dead tee can finally make two constants adjacent
//     in the op list — Fold then folds them on the next pass.
//   - PruneZeroSlotGuards turns a uniqueness guard on a never-assigned
//     slot into the constant it evaluates to, which Fold's
//     pruneConstIf then deletes the drop body of. It makes the
//     order-based argument ConstPropagate's incremental slot table
//     cannot: the table clears at a loop and after a branch.
//
// The passes converge quickly (almost always within 2–3 iterations)
// because each one only ever shrinks the op list or rewrites ops
// to a canonical-er form. A small max-iteration cap protects
// against pathological cases where convergence might oscillate.

package ir

// optimizeCleanupMaxIterations bounds how long the fixed-point loop
// can run. In practice converges in 2–3; the cap is defensive.
const optimizeCleanupMaxIterations = 8

// OptimizeCleanup runs PropagateCopies + ConstPropagate + Fold +
// ReduceStrength + PruneZeroSlotGuards to a fixed point on every
// function in prog. Each pass is idempotent on its own; the loop exists
// because they interact — the output of one can expose new work for the
// others (a strength-reduced `<expr> ; drop ; const 0` becomes a candidate
// for Fold's const + drop peephole when <expr> is itself a const).
func OptimizeCleanup(prog *Program) {
	ptrW := prog.PtrW
	if ptrW == 0 {
		ptrW = 4
	}
	for _, fn := range prog.Funcs {
		optimizeCleanupFunc(fn, ptrW)
	}
}

// optimizeCleanupFunc runs the five passes on one function until a round
// rewrites nothing. The passes are intra-function, so converging each
// function on its own is the same fixed point as converging the program;
// the difference is that a converged function is never revisited, where a
// whole-program round re-runs every pass over every function (copying its
// op list five times) for as long as any function is still changing.
func optimizeCleanupFunc(fn *Func, ptrW int) {
	for i := 0; i < optimizeCleanupMaxIterations; i++ {
		changed := false
		if next := propagateCopiesOps(fn, fn.Ops, ptrW); !opsEqual(next, fn.Ops) {
			fn.Ops = next
			changed = true
		}
		if next := constPropOps(fn.Ops, fn); !opsEqual(next, fn.Ops) {
			fn.Ops = next
			changed = true
		}
		for {
			next := foldOnce(fn.Ops)
			if opsEqual(next, fn.Ops) {
				break
			}
			fn.Ops = next
			changed = true
		}
		if next := reduceStrengthOps(fn.Ops); !opsEqual(next, fn.Ops) {
			fn.Ops = next
			changed = true
		}
		if next, ok := pruneZeroSlotGuardsIn(fn); ok {
			fn.Ops = next
			changed = true
		}
		if !changed {
			return
		}
	}
}
