package main

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/semir"
	"github.com/jakechampion/lang/internal/ssa"
)

// buildTypedArm64SSA is the opt-in migration route. Initial frontend checking,
// monomorphization and capability enforcement remain in the common driver.
// Ownership is planned on typed semantic SSA before physical RC insertion;
// neither legacy ir.LowerWith nor its AST ownership analyses run on this path.
func buildTypedArm64SSA(prog *ast.Program, info *checker.Info) (string, error) {
	typed, err := semir.BuildProgram(prog, info)
	if err != nil {
		return "", err
	}
	lowered, err := semir.LowerARM64SSA(typed)
	if err != nil {
		return "", err
	}
	entry, ok := lowered.Symbols["main"]
	if !ok {
		return "", fmt.Errorf("typed-ssa: no main function")
	}
	for name, f := range lowered.Functions {
		ssa.Optimize(f)
		if err := ssa.Verify(f); err != nil {
			return "", fmt.Errorf("typed-ssa %s after optimization: %w", name, err)
		}
	}
	return arm64ssa.EmitAsmModule(lowered.Functions, entry, arm64ssa.DefaultNumAlloc, nil)
}
