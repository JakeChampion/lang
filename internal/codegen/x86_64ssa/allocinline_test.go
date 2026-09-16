package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// The inline pop and push read the same class the helpers compute: a block
// released inline at a constant size is what __alloc hands out next for that
// class, and a block __free released is what the inline pop takes.
func TestInlineAllocationAgreesWithTheHelpersOnEveryClass(t *testing.T) {
	for _, n := range []int64{8, 16, 17, 24, 32, 33, 1000, 2033, 2048} {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		first := allocOp(f, e, n) // inline pop (an empty list, so the trampoline)
		data := f.AddOp(e, ssa.OpAdd, first, constOp(f, e, 8))
		callOp(f, e, "__fern_box_free", data, constOp(f, e, n-8)) // inline push
		viaHelper := wideCallOp(f, e, "__alloc", constOp(f, e, n))
		same1 := f.AddOp(e, ssa.OpEq, viaHelper, first)
		callOp(f, e, "__free", viaHelper, constOp(f, e, n)) // the helper's push
		again := allocOp(f, e, n)                           // inline pop
		same2 := f.AddOp(e, ssa.OpEq, again, first)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, same1, f.AddOp(e, ssa.OpShl, same2, constOp(f, e, 1))))
		if got := assembleRun(t, f, 8); got != 3 {
			t.Errorf("n=%d: helper takes the inline-released block (1) + inline takes the helper-released block (2) = %d, want 3", n, got)
		}
	}
}

// A constant size in the exact tier renders the pop and the push inline; a
// size in the large tier, or one only a register knows, stays with the
// runtime.
func TestConstantSizedAllocationsRenderInline(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	small := allocOp(f, e, 24)
	callOp(f, e, "__fern_box_free", f.AddOp(e, ssa.OpAdd, small, constOp(f, e, 8)), constOp(f, e, 16))
	f.SetRet(e, allocOp(f, e, 3000))
	asm, err := EmitAsm(f, 8)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	if strings.Contains(asm, "call "+fnLabel("__fern_box_free")) || strings.Contains(asm, "\n"+fnLabel("__fern_box_free")+":") {
		t.Errorf("a constant-size box free is still a call, or still has a body:\n%s", asm)
	}
	pops := strings.Count(asm, "mov r11, [") // the successor read of an inline pop
	if pops != 1 {
		t.Errorf("want exactly one inline pop (the 24-byte block; 3000 bytes is the large tier), found %d:\n%s", pops, asm)
	}
	if got := strings.Count(asm, "call "+allocPresSym); got != 2 {
		t.Errorf("want the trampoline at both allocation sites (the small one as its slow path), found %d:\n%s", got, asm)
	}
	if !strings.Contains(asm, "\n"+freelistSym+":") {
		t.Errorf("the freelist heads are not in the module:\n%s", asm)
	}
}

// A module whose only allocator traffic is an inline push still carries the
// list heads, or the push would name an undefined symbol.
func TestInlinePushAloneCarriesTheFreelist(t *testing.T) {
	f := ssa.NewFunc("main")
	p := f.AddParam()
	e := f.NewBlock()
	f.SetRet(e, wideCallOp(f, e, "__fern_box_free", p, constOp(f, e, 16)))
	asm, err := EmitAsmModule(map[string]*ssa.Func{"main": f}, "main", 8, []int64{0})
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	if !strings.Contains(asm, "\n"+freelistSym+":") {
		t.Errorf("the freelist heads are not in the module:\n%s", asm)
	}
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, []int64{0}); got != 0 {
		t.Errorf("box_free(null) = %d, want the pointer back (0)", got)
	}
}
