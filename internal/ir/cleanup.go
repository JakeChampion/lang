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
// ReduceStrength to a fixed point on every function in prog. Each
// pass is idempotent on its own; the loop exists because they
// interact — the output of one can expose new work for the others
// (a strength-reduced `<expr> ; drop ; const 0` becomes a candidate
// for Fold's const + drop peephole when <expr> is itself a const).
func OptimizeCleanup(prog *Program) {
	for i := 0; i < optimizeCleanupMaxIterations; i++ {
		before := snapshotPrograms(prog)
		PropagateCopies(prog)
		ConstPropagate(prog)
		Fold(prog)
		ReduceStrength(prog)
		if equalPrograms(prog, before) {
			return
		}
	}
}

// snapshotPrograms records every function's op list so the cleanup
// loop can detect "no further changes". Cheap — just a copy of the
// per-function ops slice headers; the underlying ops are immutable
// across one snapshot lifetime.
func snapshotPrograms(prog *Program) [][]Op {
	out := make([][]Op, len(prog.Funcs))
	for i, fn := range prog.Funcs {
		out[i] = append([]Op(nil), fn.Ops...)
	}
	return out
}

// equalPrograms reports whether prog's per-function op lists match
// the recorded snapshot. Convergence detection — when nothing
// changed in the last iteration, we're done.
func equalPrograms(prog *Program, snap [][]Op) bool {
	if len(prog.Funcs) != len(snap) {
		return false
	}
	for i, fn := range prog.Funcs {
		if !opsEqual(fn.Ops, snap[i]) {
			return false
		}
	}
	return true
}
