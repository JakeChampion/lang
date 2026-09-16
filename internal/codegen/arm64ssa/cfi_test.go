package arm64ssa_test

import (
	"strconv"
	"strings"
	"testing"

	arm64ssa "github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// intAfter reads the integer that follows the first occurrence of prefix,
// which is how both an operand and a `[sp, #N]` displacement are read back.
func intAfter(t *testing.T, s, prefix string) int {
	t.Helper()
	i := strings.Index(s, prefix)
	if i < 0 {
		t.Fatalf("no %q in:\n%s", prefix, s)
	}
	rest := s[i+len(prefix):]
	end := 0
	for end < len(rest) && (rest[end] == '-' || (rest[end] >= '0' && rest[end] <= '9')) {
		end++
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		t.Fatalf("no integer after %q: %v", prefix, err)
	}
	return n
}

// countDirective counts one directive across the whole module, whether or not
// it takes an operand.
func countDirective(asm, d string) int {
	return strings.Count(asm, "\t"+d+"\n") + strings.Count(asm, "\t"+d+" ")
}

// Every function carries call-frame information, and the rules are balanced:
// one startproc and one endproc, and a remembered state for every teardown,
// restored after it. Without the pairing the rule for a released frame would
// still be in effect for whatever block the layout puts next, describing those
// instructions wrongly.
func TestEveryFunctionCarriesBalancedCFI(t *testing.T) {
	// Two returns, so the layout puts a block after a teardown.
	f := ssa.NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	small := f.NewBlock()
	big := f.NewBlock()
	f.SetBrIf(entry, f.AddOp(entry, ssa.OpLt, x, constOp(f, entry, 10)), small, big)
	f.SetRet(small, constOp(f, small, 1))
	f.SetRet(big, f.AddOp(big, ssa.OpMul, x, x))

	asm, err := arm64ssa.EmitAsmModule(map[string]*ssa.Func{"f": f}, "f", 8, []int64{3})
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	starts, ends := countDirective(asm, ".cfi_startproc"), countDirective(asm, ".cfi_endproc")
	if starts == 0 {
		t.Fatal("no .cfi_startproc in the module: the image would carry no unwind data")
	}
	if starts != ends {
		t.Errorf("%d .cfi_startproc against %d .cfi_endproc", starts, ends)
	}
	if remembered, restored := countDirective(asm, ".cfi_remember_state"), countDirective(asm, ".cfi_restore_state"); remembered != restored {
		t.Errorf("%d .cfi_remember_state against %d .cfi_restore_state", remembered, restored)
	}
	// Each teardown releases the frame and says so, so the CFA offset goes
	// back to the CIE's initial 0 as many times as it was established.
	if released, taken := countDirective(asm, ".cfi_def_cfa_offset 0"), countDirective(asm, ".cfi_remember_state"); released != taken {
		t.Errorf("%d `.cfi_def_cfa_offset 0` against %d teardowns", released, taken)
	}
}

// The prologue's rules describe the frame this emitter actually builds: sp
// drops once and stays there for the body, so one CFA rule follows the
// subtraction, and x30's spill slot gets a rule of its own because a call
// makes the CIE's "the return address is still in x30" false.
func TestPrologueCFIDescribesTheFrame(t *testing.T) {
	f := ssa.NewFunc("callsOut")
	a := f.AddParam()
	e := f.NewBlock()
	f.SetRet(e, callOp(f, e, "leaf", a))

	leaf := ssa.NewFunc("leaf")
	lp := leaf.AddParam()
	lb := leaf.NewBlock()
	leaf.SetRet(lb, leaf.AddOp(lb, ssa.OpAdd, lp, constOp(leaf, lb, 1)))

	asm, err := arm64ssa.EmitAsmModule(map[string]*ssa.Func{"callsOut": f, "leaf": leaf}, "callsOut", 8, []int64{1})
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := funcText(t, asm, "callsOut")
	at := 0
	for _, want := range []string{
		"\t.cfi_startproc\n",
		"\tsub sp, sp, #",
		"\t.cfi_def_cfa_offset ",
		"\tstr x30, [sp, #",
		"\t.cfi_offset x30, -",
	} {
		i := strings.Index(body[at:], want)
		if i < 0 {
			t.Fatalf("prologue missing %q in order:\n%s", want, body)
		}
		at += i + len(want)
	}
	// The rule for x30 has to name where it actually went: the slot's offset
	// from the entry sp, which is the frame size less the slot's own offset.
	frame := intAfter(t, body, ".cfi_def_cfa_offset ")
	slot := intAfter(t, body, "\tstr x30, [sp, #")
	if got, want := intAfter(t, body, ".cfi_offset x30, "), slot-frame; got != want {
		t.Errorf(".cfi_offset x30, %d; the slot is at sp+%d in a %d-byte frame, so it is %d from the CFA", got, slot, frame, want)
	}
}

// A function that neither spills nor calls builds no frame, so it changes no
// rule and needs no bracket around its return: the CIE's initial state — the
// CFA at sp+0 with the return address in x30 — describes the whole body.
func TestFramelessFunctionCarriesNoFrameRules(t *testing.T) {
	f := ssa.NewFunc("f")
	a := f.AddParam()
	e := f.NewBlock()
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, a, a))

	asm, err := arm64ssa.EmitAsmModule(map[string]*ssa.Func{"f": f}, "f", 8, []int64{2})
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := funcText(t, asm, "f")
	if strings.Contains(body, "sub sp, sp") {
		t.Skip("this function got a frame after all, so it is not the frameless case")
	}
	for _, unwanted := range []string{".cfi_def_cfa_offset", ".cfi_remember_state", ".cfi_restore_state", ".cfi_offset"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("frameless function carries %s:\n%s", unwanted, body)
		}
	}
	if !strings.Contains(body, ".cfi_startproc") || !strings.Contains(body, ".cfi_endproc") {
		t.Errorf("frameless function carries no FDE at all:\n%s", body)
	}
}

// Balanced counts would pass a rule sitting at the wrong instruction. A
// `.cfi_def_cfa_offset 0` before the `add sp, sp, #N` says the frame is gone
// while it is still there, and a `.cfi_restore_state` before the `ret` hands
// the prologue's state to the one instruction an unwinder most wants to read
// — and both balance. So pin where each rule sits.
func TestTeardownRulesSitAtTheirInstructions(t *testing.T) {
	f := ssa.NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	small := f.NewBlock()
	big := f.NewBlock()
	f.SetBrIf(entry, f.AddOp(entry, ssa.OpLt, x, constOp(f, entry, 10)), small, big)
	f.SetRet(small, callOp(f, small, "leaf", x))
	f.SetRet(big, f.AddOp(big, ssa.OpMul, x, x))

	leaf := ssa.NewFunc("leaf")
	lp := leaf.AddParam()
	lb := leaf.NewBlock()
	leaf.SetRet(lb, leaf.AddOp(lb, ssa.OpAdd, lp, constOp(leaf, lb, 1)))

	asm, err := arm64ssa.EmitAsmModule(map[string]*ssa.Func{"f": f, "leaf": leaf}, "f", 8, []int64{3})
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	lines := strings.Split(funcText(t, asm, "f"), "\n")
	at := func(i int) string {
		if i < 0 || i >= len(lines) {
			return "(past the end of the function)"
		}
		return lines[i]
	}
	teardowns := 0
	for i, l := range lines {
		if l != "\t.cfi_remember_state" {
			continue
		}
		teardowns++
		// The restores between the two are a variable number of loads, so
		// find the next rule rather than counting instructions.
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(lines[j], "\t.cfi_") {
			j++
		}
		if got := at(j); got != "\t.cfi_def_cfa_offset 0" {
			t.Errorf("teardown remembered at line %d: want %q to close it, got %q\n%s", i, "\t.cfi_def_cfa_offset 0", got, strings.Join(lines[i:min(len(lines), j+4)], "\n"))
			continue
		}
		if got := at(j - 1); !strings.HasPrefix(got, "\tadd sp, sp, #") {
			t.Errorf("teardown at line %d releases the frame with %q, so the rule above does not describe an `add sp`", i, got)
		}
		for off, want := range map[int]string{1: "\tret", 2: "\t.cfi_restore_state"} {
			if got := at(j + off); got != want {
				t.Errorf("teardown at line %d: want %q at +%d past the rule, got %q\n%s", i, want, off, got, strings.Join(lines[i:min(len(lines), j+4)], "\n"))
			}
		}
	}
	if teardowns != 2 {
		t.Errorf("walked %d teardowns, want the 2 returns the function has", teardowns)
	}
}
