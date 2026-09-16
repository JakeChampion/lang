package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// The three rc primitives render inline: no call, no helper body when nothing
// else in the module reaches one, and the guard chain the body had.
func TestRcPrimitivesRenderInline(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	base := allocOp(f, e, 24)
	storeMem(f, e, base, 0, constOp(f, e, 1), ssa.OpStore32)
	data := f.AddOp(e, ssa.OpAdd, base, constOp(f, e, 8))
	inc := wideCallOp(f, e, "__fern_rc_inc", data)
	dec := wideCallOp(f, e, "__fern_rc_dec", inc)
	f.SetRet(e, callOp(f, e, "__fern_rc_is_unique", dec))
	asm, err := EmitAsm(f, 8)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	for _, name := range []string{"__fern_rc_inc", "__fern_rc_dec", "__fern_rc_is_unique"} {
		if strings.Contains(asm, "call "+fnLabel(name)) {
			t.Errorf("%s is still a call:\n%s", name, asm)
		}
		if strings.Contains(asm, "\n"+fnLabel(name)+":") {
			t.Errorf("%s still has a body nothing calls:\n%s", name, asm)
		}
	}
	for _, want := range []string{"add dword ptr [", "sub dword ptr [", "sete ", "add dword ptr [rip + " + rcUnderflowSym + "], 1", "\n" + rcUnderflowSym + ":"} {
		if !strings.Contains(asm, want) {
			t.Errorf("inline rc sequences lack %q:\n%s", want, asm)
		}
	}
	if got := assembleRun(t, f, 8); got != 1 {
		t.Errorf("is_unique after inc then dec = %d, want 1", got)
	}
}

// A literal lives under a static sentinel: inc and dec leave it and is_unique
// says no. A count released past zero is counted as an over-release and left
// at zero rather than wrapped.
func TestInlineRcGuardsMatchTheHelpers(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	lit := constStr(f, e, "abc")
	callOp(f, e, "__fern_rc_inc", lit)
	callOp(f, e, "__fern_rc_dec", lit)
	litUnique := callOp(f, e, "__fern_rc_is_unique", lit)
	litRc := loadMem(f, e, lit, -8, ssa.OpLoad32U)
	litKept := f.AddOp(e, ssa.OpEq, litRc, constOp(f, e, 0x80000000))
	base := allocOp(f, e, 24)
	storeMem(f, e, base, 0, constOp(f, e, 0), ssa.OpStore32) // already released
	data := f.AddOp(e, ssa.OpAdd, base, constOp(f, e, 8))
	callOp(f, e, "__fern_rc_dec", data)
	callOp(f, e, "__fern_rc_dec", data)
	over := callOp(f, e, "__fern_rc_underflow_count")
	overTwo := f.AddOp(e, ssa.OpEq, over, constOp(f, e, 2))
	stillZero := f.AddOp(e, ssa.OpEq, loadMem(f, e, data, -8, ssa.OpLoad32U), constOp(f, e, 0))
	sum := f.AddOp(e, ssa.OpAdd, f.AddOp(e, ssa.OpShl, litUnique, constOp(f, e, 3)), litKept)
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, overTwo, constOp(f, e, 1)))
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, stillZero, constOp(f, e, 2)))
	f.SetRet(e, sum)
	if got := assembleRun(t, f, 8); got != 7 {
		t.Errorf("literal kept (1) + two over-releases counted (2) + count still zero (4) + literal unique (8) = %d, want 7", got)
	}
}
