package arm64ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// emitIdxAsm renders a module, failing the test rather than returning an error.
func emitIdxAsm(t *testing.T, funcs map[string]*ssa.Func, entry string) string {
	t.Helper()
	asm, err := arm64ssa.EmitAsmModule(funcs, entry, arm64ssa.DefaultNumAlloc, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	return asm
}

// An array index is address arithmetic — four instructions or fewer — so a call
// costs more than the work: the allocator has to spill every caller-saved
// register holding a value live across it, and indexing sits in the innermost
// loop of anything that walks an array. cmp.sort's monomorphised body made 22
// such calls where the stack-machine backend makes none.
//
// The bounds check has to survive inlining, and it is load-bearing beyond the
// trap: `cmp w` rejects a negative index as a huge unsigned, which is what makes
// the full-width add below it safe.
func TestArrayIndexIsInlinedNotCalled(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	arr := addrCallOp(f, b, "__alloc_u8", constOp(f, b, 16))
	elem := addrCallOp(f, b, "__arr_idx_1", arr, constOp(f, b, 0))
	f.SetRet(b, load8u(f, b, elem, 0))

	asm := emitIdxAsm(t, map[string]*ssa.Func{"main": f}, "main")
	if strings.Contains(asm, "bl "+"fn___arr_idx") {
		t.Error("array index still emitted as a call")
	}
	for _, want := range []string{"ldur", "cmp", "b.lo", "#134"} {
		if !strings.Contains(asm, want) {
			t.Errorf("inlined index is missing %q — the bounds check must survive", want)
		}
	}
}

// Every site needs its own ok-label: two indexes in one function otherwise emit
// the same label twice and the assembler rejects the module.
func TestTwoIndexesInOneFunctionGetDistinctLabels(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	arr := addrCallOp(f, b, "__alloc_u8", constOp(f, b, 16))
	a := load8u(f, b, addrCallOp(f, b, "__arr_idx_1", arr, constOp(f, b, 0)), 0)
	c := load8u(f, b, addrCallOp(f, b, "__arr_idx_1", arr, constOp(f, b, 1)), 0)
	f.SetRet(b, f.AddOp(b, ssa.OpAdd, a, c))

	asm := emitIdxAsm(t, map[string]*ssa.Func{"main": f}, "main")
	seen := map[string]bool{}
	for _, l := range strings.Split(asm, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, ".Lssa_idx_") || !strings.HasSuffix(l, ":") {
			continue
		}
		if seen[l] {
			t.Errorf("duplicate index label %q — the assembler rejects a repeated label", l)
		}
		seen[l] = true
	}
	if len(seen) < 2 {
		t.Errorf("expected two inlined index sites, saw %d: %v", len(seen), seen)
	}
}
