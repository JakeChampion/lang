// The optimisation battery every backend runs between lowering and
// emission, in one place because it was previously copied into six.
//
// The copies drifted: the natives swept dead code before the cleanup
// that creates it and wasm after, and a comment in the wasm backend
// asked whoever added a pass to remember to wire it into the two
// natives by hand. A shared entry point makes that structural — a pass
// added here reaches every backend, including the ones whose author did
// not think to look.

package ir

// OptimizeProgram runs the whole battery on prog. ptrW is the target's
// pointer width in bytes, which sizes the closure pair the
// defunctionaliser rewrites (16 bytes with env_ptr at +8 on the
// 64-bit natives, 8 bytes at +4 on wasm32).
//
// Callers that root externally-reachable functions — a `-shared` export,
// a wasm world export — must call MarkExternallyReachable BEFORE this,
// so Inline's size policy does not read an export's sole in-program
// caller as its last reference. The dead-function cull stays with the
// caller: its root set is per-backend, and so is the alias map it walks.
func OptimizeProgram(prog *Program, ptrW int32) {
	// Tail-call optimisation: `OpCallDirect <self> ; OpReturn` becomes a
	// parameter rebind plus `OpBr` back to the entry, so a self-recursive
	// function runs in O(1) stack instead of growing one frame per call.
	TailCallOptimize(prog)
	// Inline runs twice, around Defunctionalise. The first pass exposes
	// constants for the cleanup to fold; the second catches the direct
	// calls defunctionalisation has just made out of indirect ones, which
	// the first could not see. ir.inlineMaxUnitOps is the whole-program
	// size ceiling above which the pass declines.
	Inline(prog)
	// Defunctionalise + ElideClosurePair turn indirect closure calls into
	// direct ones wherever the closure flow is monomorphic enough to prove
	// the target statically, collapsing the closure pair to a single
	// env_ptr. What does not defunctionalise still falls back to the
	// OpConstFunc / OpCallIndirect path.
	Defunctionalise(prog, ptrW)
	ElideClosurePair(prog, ptrW)
	// A zero-capture closure escaping past ElideClosurePair — passed as a
	// function-typed argument, say — becomes OpConstFunc, so it
	// materialises against a static .rodata cell rather than a
	// heap-allocated pair.
	InlineZeroCaptureClosures(prog)
	Inline(prog)
	OptimizeFunctions(prog)
}

// OptimizeFunctions runs the per-function tail of the battery: the
// rewrites that need no whole-program information.
//
// It is separate because internal/fernrt runs only this half. That
// package hands out runtime helpers looked up BY NAME, so a pass that
// inlines a helper into its sole caller and culls the original would
// remove the very function a later lookup asks for.
func OptimizeFunctions(prog *Program) {
	// FuseTee folds store+reload into OpTeeLocal.
	FuseTee(prog)
	// DCE precedes FlattenBranches because flattening requires the
	// then-arm's last op before its OpEnd to BE the return: a dead tail
	// after that return disqualifies the arm, so sweeping first is what
	// lets it flatten.
	EliminateDeadCode(prog)
	// FlattenBranches merges `if c { return X; } return Y` into one typed
	// if with a single trailing return.
	FlattenBranches(prog)
	// OptimizeCleanup is the copyprop / constprop / Fold / strength /
	// zero-slot-guard / DCE fixpoint. It runs last because it is the pass
	// that profits from every rewrite above, and it sweeps the dead code
	// its own folding creates.
	OptimizeCleanup(prog)
}
