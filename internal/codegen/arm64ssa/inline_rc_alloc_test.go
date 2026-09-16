package arm64ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

func allocOp(f *ssa.Func, b *ssa.Block, size int64) ssa.Value {
	return f.AddOp(b, ssa.OpAlloc, constOp(f, b, size))
}

// The three rc primitives render inline: no call, no helper body when nothing
// else in the module reaches one, and the guard chain the body had. A fresh
// cell is unique after an inc and a dec.
func TestRcPrimitivesRenderInline(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	base := allocOp(f, e, 24)
	store32(f, e, base, constOp(f, e, 1), 0)
	data := f.AddOp(e, ssa.OpAdd, base, constOp(f, e, 8))
	inc := addrCallOp(f, e, "__fern_rc_inc", data)
	dec := addrCallOp(f, e, "__fern_rc_dec", inc)
	f.SetRet(e, callOp(f, e, "__fern_rc_is_unique", dec))
	asm, err := arm64ssa.EmitAsmModule(map[string]*ssa.Func{"main": f}, "main", arm64ssa.DefaultNumAlloc, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	for _, name := range []string{"__fern_rc_inc", "__fern_rc_dec", "__fern_rc_is_unique"} {
		if strings.Contains(asm, "bl fn_"+name) {
			t.Errorf("%s is still a call:\n%s", name, asm)
		}
		if strings.Contains(asm, "\nfn_"+name+":") {
			t.Errorf("%s still has a body nothing calls:\n%s", name, asm)
		}
	}
	for _, want := range []string{"cset ", "tbnz ", "stur ", "__fern_rc_underflow:"} {
		if !strings.Contains(asm, want) {
			t.Errorf("inline rc sequences lack %q:\n%s", want, asm)
		}
	}
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", arm64ssa.DefaultNumAlloc); got != 1 {
		t.Errorf("is_unique after inc then dec = %d, want 1", got)
	}
}

// A literal lives under a static sentinel: inc and dec leave it and is_unique
// says no. A count released past zero is counted as an over-release and left
// at zero.
func TestInlineRcGuardsMatchTheHelpers(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	lit := constStr(f, e, "abc")
	rcBefore := load32u(f, e, lit, -8)
	callOp(f, e, "__fern_rc_inc", lit)
	callOp(f, e, "__fern_rc_dec", lit)
	litUnique := callOp(f, e, "__fern_rc_is_unique", lit)
	litKept := f.AddOp(e, ssa.OpEq, load32u(f, e, lit, -8), rcBefore)
	base := allocOp(f, e, 24)
	store32(f, e, base, constOp(f, e, 0), 0) // already released
	data := f.AddOp(e, ssa.OpAdd, base, constOp(f, e, 8))
	callOp(f, e, "__fern_rc_dec", data)
	callOp(f, e, "__fern_rc_dec", data)
	overTwo := f.AddOp(e, ssa.OpEq, callOp(f, e, "__fern_rc_underflow_count"), constOp(f, e, 2))
	stillZero := f.AddOp(e, ssa.OpEq, load32u(f, e, data, -8), constOp(f, e, 0))
	sum := f.AddOp(e, ssa.OpAdd, f.AddOp(e, ssa.OpShl, litUnique, constOp(f, e, 3)), litKept)
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, overTwo, constOp(f, e, 1)))
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, stillZero, constOp(f, e, 2)))
	f.SetRet(e, sum)
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", arm64ssa.DefaultNumAlloc); got != 7 {
		t.Errorf("literal kept (1) + two over-releases counted (2) + count still zero (4) + literal unique (8) = %d, want 7", got)
	}
}

// The inline pop and push read the same class the helpers compute: a block
// released inline is what __alloc hands out next for that class, and a block
// __free released is what the inline pop takes.
func TestInlineAllocationAgreesWithTheHelpersOnEveryClass(t *testing.T) {
	for _, n := range []int64{8, 16, 17, 24, 32, 33, 1000, 2033, 2048} {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		first := allocOp(f, e, n)
		data := f.AddOp(e, ssa.OpAdd, first, constOp(f, e, 8))
		callOp(f, e, "__fern_box_free", data, constOp(f, e, n-8))
		viaHelper := addrCallOp(f, e, "__alloc", constOp(f, e, n))
		same1 := f.AddOp(e, ssa.OpEq, viaHelper, first)
		callOp(f, e, "__free", viaHelper, constOp(f, e, n))
		again := allocOp(f, e, n)
		same2 := f.AddOp(e, ssa.OpEq, again, first)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, same1, f.AddOp(e, ssa.OpShl, same2, constOp(f, e, 1))))
		if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", arm64ssa.DefaultNumAlloc); got != 3 {
			t.Errorf("n=%d: helper takes the inline-released block (1) + inline takes the helper-released block (2) = %d, want 3", n, got)
		}
	}
}

// A constant size in the exact tier renders the pop and the push inline; a
// size in the large tier stays with the trampoline alone.
func TestConstantSizedAllocationsRenderInline(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	small := allocOp(f, e, 24)
	callOp(f, e, "__fern_box_free", f.AddOp(e, ssa.OpAdd, small, constOp(f, e, 8)), constOp(f, e, 16))
	f.SetRet(e, allocOp(f, e, 3000))
	asm, err := arm64ssa.EmitAsmModule(map[string]*ssa.Func{"main": f}, "main", arm64ssa.DefaultNumAlloc, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	if strings.Contains(asm, "bl fn___fern_box_free") || strings.Contains(asm, "\nfn___fern_box_free:") {
		t.Errorf("a constant-size box free is still a call, or still has a body:\n%s", asm)
	}
	if pops := strings.Count(asm, "ldr x17, ["); pops != 1 {
		t.Errorf("want exactly one inline pop (the 24-byte block; 3000 bytes is the large tier), found %d:\n%s", pops, asm)
	}
	if got := strings.Count(asm, "bl __ssa_alloc_pres"); got != 2 {
		t.Errorf("want the trampoline at both allocation sites (the small one as its slow path), found %d:\n%s", got, asm)
	}
}
