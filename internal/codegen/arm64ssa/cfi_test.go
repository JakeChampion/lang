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

// twoReturnsWithAFrame is a function with two returns, so one of them is not
// the block the layout puts last, and one call, so it has a frame to describe.
// A frameless function changes no rule, and a fixture without a frame turns
// every count below into 0 against 0.
func twoReturnsWithAFrame() map[string]*ssa.Func {
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
	return map[string]*ssa.Func{"f": f, "leaf": leaf}
}

// Every function carries call-frame information, and the rules are balanced:
// one startproc and one endproc, and one epilogue rule for the one epilogue.
// Nothing needs remembering and restoring, because the epilogue is the last
// thing in the function and no block follows it for a stale rule to describe.
func TestEveryFunctionCarriesBalancedCFI(t *testing.T) {
	asm, err := arm64ssa.EmitAsmModule(twoReturnsWithAFrame(), "f", 8, []int64{3})
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
	// A bracket is what a teardown at each return site needed. One epilogue at
	// the end of the function needs none, and emitting them anyway would be
	// .eh_frame nothing reads.
	if remembered, restored := countDirective(asm, ".cfi_remember_state"), countDirective(asm, ".cfi_restore_state"); remembered != 0 || restored != 0 {
		t.Errorf("%d .cfi_remember_state and %d .cfi_restore_state, want none: there is one epilogue and nothing follows it", remembered, restored)
	}
	// Each framed function releases its frame once and says so once. The
	// fixture's `f` has a frame; nothing below distinguishes "balanced" from
	// "absent", so say outright that it reached the code under test.
	released := countDirective(asm, ".cfi_def_cfa_offset 0")
	if released == 0 {
		t.Fatal("no `.cfi_def_cfa_offset 0` in the module: the fixture built no frame, so the checks here would compare 0 against 0")
	}
	if taken := countDirective(asm, ".cfi_def_cfa_offset") - released; released != taken {
		t.Errorf("%d frames established against %d released", taken, released)
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
	// The rule has to describe the stack the instructions actually build, so
	// the frame size comes from the `sub sp` operands and not from the
	// directive being checked. Reading it back out of `.cfi_def_cfa_offset`
	// would let an emitter whose subtraction and CFA offset disagree pass:
	// the CFI would describe a frame sp never drops by, and every unwind past
	// it would read whatever is really there.
	frame := spAdjustTotal(t, body, "sub", body[:strings.Index(body, "\t.cfi_def_cfa_offset ")])
	if got := intAfter(t, body, ".cfi_def_cfa_offset "); got != frame {
		t.Errorf(".cfi_def_cfa_offset %d, but the prologue subtracts %d from sp:\n%s", got, frame, body)
	}
	// The epilogue has to give back exactly what the prologue took, or the
	// CFA is wrong for the caller's frame rather than this one.
	if got := spAdjustTotal(t, body, "add", body); got != frame {
		t.Errorf("the epilogue adds %d back to sp against the prologue's %d:\n%s", got, frame, body)
	}
	// The rule for x30 has to name where it actually went: the slot's offset
	// from the entry sp, which is the frame size less the slot's own offset.
	slot := intAfter(t, body, "\tstr x30, [sp, #")
	if got, want := intAfter(t, body, ".cfi_offset x30, "), slot-frame; got != want {
		t.Errorf(".cfi_offset x30, %d; the slot is at sp+%d in a %d-byte frame, so it is %d from the CFA", got, slot, frame, want)
	}
}

// spAdjustTotal sums every `<op> sp, sp, #N` in text. A frame past 4095 bytes
// is split across two instructions (spAdjustLines), so one line is not enough
// to read the size off.
func spAdjustTotal(t *testing.T, whole, op, text string) int {
	t.Helper()
	total, found := 0, false
	for _, l := range strings.Split(text, "\n") {
		if !strings.HasPrefix(l, "\t"+op+" sp, sp, #") {
			continue
		}
		found = true
		total += intAfter(t, l, "#")
	}
	if !found {
		t.Fatalf("no `%s sp, sp, #` in:\n%s", op, whole)
	}
	return total
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
// while it is still there, and a `ret` before the rule returns with the CFA
// still described against a frame the caller owns again — and both leave the
// counts untouched. So pin where each rule sits.
func TestEpilogueRulesSitAtTheirInstructions(t *testing.T) {
	asm, err := arm64ssa.EmitAsmModule(twoReturnsWithAFrame(), "f", 8, []int64{3})
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := funcText(t, asm, "f")
	// The epilogue is the tail of the function: the frame release, the rule
	// that describes it, the return, and then the end of the FDE.
	if want := "\t.cfi_def_cfa_offset 0\n\tret\n\t.cfi_endproc"; !strings.Contains(body, want) {
		t.Errorf("the function does not end in %q:\n%s", want, body)
	}
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if l != "\t.cfi_def_cfa_offset 0" {
			continue
		}
		if i == 0 || !strings.HasPrefix(lines[i-1], "\tadd sp, sp, #") {
			prev := "(nothing)"
			if i > 0 {
				prev = lines[i-1]
			}
			t.Errorf("`.cfi_def_cfa_offset 0` follows %q, not the `add sp` that releases the frame:\n%s", prev, body)
		}
	}
	// Both returns reach that one epilogue: the block the layout puts last
	// falls into it, the other branches to it.
	if branches, rets := strings.Count(body, "\tb .Lfn_f_epi\n"), strings.Count(body, "\tret\n"); branches != 1 || rets != 1 {
		t.Errorf("%d branches to the epilogue and %d `ret` for a function with two returns, want 1 and 1:\n%s", branches, rets, body)
	}
}
