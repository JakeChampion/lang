package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// Every function carries call-frame information, and the rules are balanced:
// one startproc and one endproc, and one epilogue rule for the one epilogue.
// Nothing needs remembering and restoring, because the epilogue is the last
// thing in the function and no block follows it for a stale rule to describe.
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
	// A bracket is what a teardown at each return site needed. One epilogue at
	// the end of the function needs none, and emitting them anyway would be
	// .eh_frame nothing reads.
	if remembered, restored := count(".cfi_remember_state"), count(".cfi_restore_state"); remembered != 0 || restored != 0 {
		t.Errorf("%d .cfi_remember_state and %d .cfi_restore_state, want none: there is one epilogue and nothing follows it", remembered, restored)
	}
	// Every `ret` is an epilogue, and each one re-describes the CFA before it,
	// since rsp is no longer reachable through rbp by then. With one epilogue
	// per function there are as many of each as there are functions.
	rets, defs := strings.Count(asm, "\tret\n"), count(".cfi_def_cfa rsp, 8")
	if defs != rets {
		t.Errorf("%d ret against %d `.cfi_def_cfa rsp, 8`", rets, defs)
	}
	if rets != starts {
		t.Errorf("%d ret across %d functions: a function returns through its one epilogue", rets, starts)
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

// fnBody slices the module down to one function's FDE: its label through the
// .cfi_endproc that closes it.
func fnBody(t *testing.T, asm, name string) string {
	t.Helper()
	start := strings.Index(asm, fnLabel(name)+":")
	if start < 0 {
		t.Fatalf("no %s: label in module", fnLabel(name))
	}
	end := strings.Index(asm[start:], "\t.cfi_endproc")
	if end < 0 {
		t.Fatalf("%s carries no .cfi_endproc", fnLabel(name))
	}
	return asm[start : start+end]
}

// Balanced counts would pass a rule sitting at the wrong instruction. A
// `.cfi_def_cfa rsp, 8` before `pop rbp` describes rsp as 8 past the CFA while
// it is still the frame pointer, and a `ret` before the rule leaves the CFA
// described through an rbp the caller owns again — and both leave the counts
// untouched. So pin where each rule sits.
func TestEpilogueRulesSitAtTheirInstructions(t *testing.T) {
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
	body := fnBody(t, asm, "f")
	// The epilogue is the tail of the function: the CFA rule sits between the
	// `pop rbp` that invalidates the old one and the `ret` that uses it, and
	// nothing follows.
	if want := "\tmov rsp, rbp\n\tpop rbp\n\t.cfi_def_cfa rsp, 8\n\tret\n"; !strings.HasSuffix(body, want) {
		t.Errorf("function does not end in the epilogue %q:\n%s", want, body)
	}
	// Both returns reach that one epilogue: the block the layout puts last
	// falls into it, the other branches to it.
	if jumps, rets := strings.Count(body, "\tjmp .L_"+fnLabel("f")+"_epi\n"), strings.Count(body, "\tret\n"); jumps != 1 || rets != 1 {
		t.Errorf("%d jumps to the epilogue and %d `ret` for a function with two returns, want 1 and 1:\n%s", jumps, rets, body)
	}
}
