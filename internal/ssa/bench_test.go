package ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/ssa"
)

// loadPreludeIR parses + checks + lowers a minimal main
// program once for the benchmarks below. Auto-prelude pulls
// in ~710 functions; the IR lowering itself is non-trivial
// so we keep this work outside the timed loop.
func loadPreludeIR(b *testing.B) []*ir.Func {
	b.Helper()
	src := `function main(): i32 { return 0; }`
	prog, err := parser.Parse(src)
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		b.Fatalf("check: %v", err)
	}
	irProg, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		b.Fatalf("lower: %v", err)
	}
	return irProg.Funcs
}

// BenchmarkLiftPrelude — lift every prelude function in each
// iteration. Establishes the baseline cost of the lift on a
// realistic workload (~710 functions).
func BenchmarkLiftPrelude(b *testing.B) {
	funcs := loadPreludeIR(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, fn := range funcs {
			_, err := ssa.LiftFromIR(fn)
			if err != nil {
				b.Fatalf("LiftFromIR(%s): %v", fn.Name, err)
			}
		}
	}
}

// BenchmarkOptimizePrelude — given pre-lifted SSA Funcs, run
// Optimize on a Clone of each in each iteration. Separates
// the cost of Optimize from the cost of lifting.
func BenchmarkOptimizePrelude(b *testing.B) {
	funcs := loadPreludeIR(b)
	lifted := make([]*ssa.Func, 0, len(funcs))
	for _, fn := range funcs {
		f, err := ssa.LiftFromIR(fn)
		if err != nil {
			b.Fatalf("LiftFromIR(%s): %v", fn.Name, err)
		}
		lifted = append(lifted, f)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, f := range lifted {
			ssa.Optimize(f.Clone())
		}
	}
}

// BenchmarkLiftAndOptimizePrelude — combined end-to-end:
// lift + Optimize on every prelude function. Useful as a
// regression guard for the whole front-of-pipeline cost.
func BenchmarkLiftAndOptimizePrelude(b *testing.B) {
	funcs := loadPreludeIR(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, fn := range funcs {
			f, err := ssa.LiftFromIR(fn)
			if err != nil {
				b.Fatalf("LiftFromIR(%s): %v", fn.Name, err)
			}
			ssa.Optimize(f)
		}
	}
}
