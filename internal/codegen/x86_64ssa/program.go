package x86_64ssa

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/ssa"
)

// EmitProgram lowers a whole checked program to a runnable x86-64 GAS program
// via the SSA register-allocated path: ir.LowerWith → ssa.LiftFromIR per
// function → EmitAsmModule. The `main` function is the entry; the emitted
// `_start` calls it and exits with its i32 return value.
//
// This is the first whole-program step of phase-2 slice 3c. Scope: the
// integer/no-runtime subset — programs whose entire call graph lifts and emits
// through the SSA path. Programs that pull in ops the emitter doesn't yet cover
// at the whole-program level (RC inc/dec, runtime helpers, closure dispatch)
// return a clear error rather than miscompiling; those capabilities layer on as
// slice 3c proceeds. The caller is expected to have run monomorphisation first
// (as the interp and stack-machine paths do).
func EmitProgram(prog *ast.Program, info *checker.Info, numAlloc int) (string, error) {
	irProg, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		return "", fmt.Errorf("x86_64ssa: lower: %w", err)
	}
	funcs := make(map[string]*ssa.Func, len(irProg.Funcs))
	hasMain := false
	for _, fn := range irProg.Funcs {
		f, err := ssa.LiftFromIR(fn)
		if err != nil {
			return "", fmt.Errorf("x86_64ssa: lift %q: %w", fn.Name, err)
		}
		// Same three steps, in the same order, as the shipping SSA backends
		// (buildArm64SSA / buildWasmSSA in cmd/fern): lift, optimise, then
		// verify. This path used to stop after the lift, which made every
		// SSA-vs-stack-machine figure measured through it — including the ones
		// docs/SSA-DECISION.md rests on — a measurement of a pipeline no user
		// can invoke (#6979).
		//
		// Verify runs AFTER Optimize for the reason buildArm64SSA gives at
		// length: the lifter deliberately leaves unreachable blocks for
		// PruneUnreachable to drop, and Verify's use-before-def rule wants a
		// def in an ancestor block, which nothing in an unreachable block has.
		// It is what keeps this path's "unsupported constructs error rather
		// than miscompile" promise from being only a comment.
		ssa.Optimize(f)
		if err := ssa.Verify(f); err != nil {
			return "", fmt.Errorf("x86_64ssa: verify %q: %w", fn.Name, err)
		}
		funcs[fn.Name] = f
		if fn.Name == "main" {
			hasMain = true
		}
	}
	if !hasMain {
		return "", fmt.Errorf("x86_64ssa: program has no main function")
	}
	return EmitAsmModule(funcs, "main", numAlloc, nil)
}
