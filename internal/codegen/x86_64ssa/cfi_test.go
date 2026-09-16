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
// it is still the frame pointer, and a `.cfi_restore_state` before the `ret`
// hands the prologue's state to the one instruction an unwinder most wants to
// read — and both balance. So pin where each rule sits, which is what the
// stack-machine emitter's own test does for its single epilogue.
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
	lines := strings.Split(fnBody(t, asm, "f"), "\n")
	at := func(i int) string {
		if i < 0 || i >= len(lines) {
			return "(past the end of the function)"
		}
		return lines[i]
	}
	epilogues := 0
	for i, l := range lines {
		if l != "\t.cfi_remember_state" {
			continue
		}
		epilogues++
		// The teardown between the two is a variable number of pops, so find
		// the next rule rather than counting instructions.
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(lines[j], "\t.cfi_") {
			j++
		}
		for off, want := range map[int]string{
			-1: "\tpop rbp",
			0:  "\t.cfi_def_cfa rsp, 8",
			1:  "\tret",
			2:  "\t.cfi_restore_state",
		} {
			if got := at(j + off); got != want {
				t.Errorf("epilogue remembered at line %d: want %q, got %q\n%s", i, want, got, strings.Join(lines[i:min(len(lines), j+4)], "\n"))
			}
		}
	}
	if epilogues != 2 {
		t.Errorf("walked %d epilogues, want the 2 returns the function has", epilogues)
	}
}
