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
// function in prog. Each
// pass is idempotent on its own; the loop exists because they
// interact — the output of one can expose new work for the others
// (a strength-reduced `<expr> ; drop ; const 0` becomes a candidate
// for Fold's const + drop peephole when <expr> is itself a const).
func OptimizeCleanup(prog *Program) {
	for i := 0; i < optimizeCleanupMaxIterations; i++ {
		// Run all four every iteration (they interact — no short-circuit)
		// and OR their changed verdicts. Converged once a full round
		// rewrote nothing. This replaces the old snapshotPrograms +
		// equalPrograms convergence check, which deep-COPIED the entire
		// program's op lists every iteration — an up-to-8× whole-program
		// duplication that dominated self-host driver build time and kept
		// this fixpoint off the native backends (#4377 slice 1b). Each
		// sub-pass now reports whether it changed anything (a per-function
		// opsEqual, no copy), so the loop needs no snapshot at all.
		c1 := PropagateCopies(prog)
		c2 := ConstPropagate(prog)
		c3 := Fold(prog)
		c4 := ReduceStrength(prog)
		c5 := PruneZeroSlotGuards(prog)
		if !(c1 || c2 || c3 || c4 || c5) {
			return
		}
	}
}
