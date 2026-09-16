package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// Every function carries call-frame information, and the rules are balanced:
// one startproc and one endproc, and a remembered state for every epilogue,
// restored after it. Without the pairing a rule meant for the few instructions
// of one epilogue would still be in effect for whatever block the layout puts
// next, describing those instructions wrongly.
func TestEveryFunctionCarriesBalancedCFI(t *testing.T) {
	// Two returns, so the layout puts a block after an epilogue.
	f := ssa.NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	small := f.NewBlock()
	big := f.NewBlock()
	f.SetBrIf(entry, f.AddOp(entry, ssa.OpLt, x, constOp(f, entry, 10)), small, big)
	f.SetRet(small, constOp(f, small, 1))
	f.SetRet(big, f.AddOp(big, ssa.OpMul, x, x))

	asm, err := EmitAsmModule(map[string]*ssa.Func{"f": f}, "f", 8, []int64{3})
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	count := func(d string) int { return strings.Count(asm, "\t"+d+"\n") + strings.Count(asm, "\t"+d+" ") }

	starts, ends := count(".cfi_startproc"), count(".cfi_endproc")
	if starts == 0 {
		t.Fatal("no .cfi_startproc in the module: the image would carry no unwind data")
	}
	if starts != ends {
		t.Errorf("%d .cfi_startproc against %d .cfi_endproc", starts, ends)
	}
	if remembered, restored := count(".cfi_remember_state"), count(".cfi_restore_state"); remembered != restored {
		t.Errorf("%d .cfi_remember_state against %d .cfi_restore_state", remembered, restored)
	}
	// Every `ret` is an epilogue, and each one re-describes the CFA before it,
	// since rsp is no longer reachable through rbp by then.
	if rets, defs := strings.Count(asm, "\tret\n"), count(".cfi_def_cfa rsp, 8"); defs != rets {
		t.Errorf("%d ret against %d `.cfi_def_cfa rsp, 8`", rets, defs)
	}
}

// The prologue's rules describe the frame this emitter actually builds: the
// CFA moves when rbp is pushed, and is tracked through rbp from the point it
// is established, which is what keeps the reservation and the callee-saved
// pushes below it silent.
func TestPrologueCFIDescribesTheFrame(t *testing.T) {
	f := ssa.NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	e := f.NewBlock()
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, a, b))

	asm, err := EmitAsmModule(map[string]*ssa.Func{"f": f}, "f", 8, []int64{1, 2})
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := asm[strings.Index(asm, fnLabel("f")+":"):]
	want := []string{
		"\t.cfi_startproc\n",
		"\tpush rbp\n",
		"\t.cfi_def_cfa_offset 16\n",
		"\t.cfi_offset rbp, -16\n",
		"\tmov rbp, rsp\n",
		"\t.cfi_def_cfa_register rbp\n",
	}
	at := 0
	for _, w := range want {
		i := strings.Index(body[at:], w)
		if i < 0 {
			t.Fatalf("prologue missing %q in order:\n%s", w, body[:200])
		}
		at += i + len(w)
	}
}
