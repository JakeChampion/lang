package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// __fern_rc_is_unique on the real-asm path: a freshly rc-headed heap cell
// (rc == 1) is unique; a null or low-address non-pointer scalar is not. Exercises
// the rc-header the SSA bump allocator now lays down (rc=1 at data-8) plus the
// gated runtime-helper emission. See docs/SSA-RC-RUNTIME.md.
func TestAsmRunRcIsUnique(t *testing.T) {
	// isUniqueOf builds main() = __fern_rc_is_unique(<arg>) where arg is produced
	// by mkArg, and returns the native exit code.
	isUniqueOf := func(mkArg func(f *ssa.Func, b *ssa.Block) ssa.Value) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		f.SetRet(e, callOp(f, e, "__fern_rc_is_unique", mkArg(f, e)))
		return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
	}

	// A freshly allocated heap block (env cell) carries rc == 1 -> unique.
	if got := isUniqueOf(func(f *ssa.Func, b *ssa.Block) ssa.Value {
		return makeEnvOp(f, b, constOp(f, b, 7))
	}); got != 1 {
		t.Errorf("is_unique(fresh cell) = %d, want 1", got)
	}

	// A null pointer is not a heap value -> 0.
	if got := isUniqueOf(func(f *ssa.Func, b *ssa.Block) ssa.Value {
		return constOp(f, b, 0)
	}); got != 0 {
		t.Errorf("is_unique(null) = %d, want 0", got)
	}

	// A low-address non-pointer scalar (below the 0x10000 guard) -> 0.
	if got := isUniqueOf(func(f *ssa.Func, b *ssa.Block) ssa.Value {
		return constOp(f, b, 42)
	}); got != 0 {
		t.Errorf("is_unique(42) = %d, want 0", got)
	}
}
