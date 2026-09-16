package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// calleeSavedIn counts a register at any width and never inside a longer word:
// a label carrying a register name and a helper symbol are not uses.
func TestCalleeSavedInTokenisesRegisterNames(t *testing.T) {
	asm := "fn_r13_helper:\n\tmov r12d, [rbx+8]\n\tjmp .Lr14_done\n\tadd r15b, 1\n\tcall __fern_r13\n\tmov rax, 4r14x\n"
	want := []int{gpIndex("rbx"), gpIndex("r12"), gpIndex("r15")}
	if got := calleeSavedIn(asm, len(gpRegs)); !equalInts(got, want) {
		t.Fatalf("calleeSavedIn = %v, want %v", got, want)
	}
	// Registers above the program's file are not allocatable, so a mention of
	// one cannot be this function's.
	if got, want := calleeSavedIn(asm, gpIndex("r15")), want[:2]; !equalInts(got, want) {
		t.Fatalf("calleeSavedIn with a %d-register file = %v, want %v", gpIndex("r15"), got, want)
	}
	if got := calleeSavedIn("", len(gpRegs)); len(got) != 0 {
		t.Fatalf("calleeSavedIn(empty) = %v, want none", got)
	}
}

// The restore marker the body is emitted with never reaches the output: every
// return carries the pops instead, in reverse push order.
func TestRestoreMarkerIsReplacedByPops(t *testing.T) {
	g := ssa.NewFunc("g")
	gx := g.AddParam()
	ge := g.NewBlock()
	g.SetRet(ge, g.AddOp(ge, ssa.OpMul, gx, constOp(g, ge, 2)))

	// x lives across the call, so it gets a callee-saved home and h pushes it.
	h := ssa.NewFunc("h")
	x := h.AddParam()
	he := h.NewBlock()
	call := h.AddOp(he, ssa.OpCall, x)
	he.Ops[len(he.Ops)-1].Str = "g"
	h.SetRet(he, h.AddOp(he, ssa.OpAdd, call, x))

	asm, err := EmitAsmModule(map[string]*ssa.Func{"g": g, "h": h}, "h", 8, []int64{5})
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	if strings.Contains(asm, restoreMarker) {
		t.Fatalf("restore marker survived into the output:\n%s", asm)
	}
	body := asm[strings.Index(asm, fnLabel("h")+":"):]
	if i := strings.Index(body, "\n"+fnLabel("g")+":"); i >= 0 {
		body = body[:i]
	}
	pushes := strings.Count(body, "\tpush ") - strings.Count(body, "\tpush rbp")
	pops := strings.Count(body, "\tpop ") - strings.Count(body, "\tpop rbp")
	if pushes == 0 || pops != pushes {
		t.Fatalf("h pushes %d callee-saved registers and pops %d:\n%s", pushes, pops, body)
	}
}

// writeBody hands the body to the writer a line at a time, so the writer's
// self-move filter still sees each line, and replaces every marker with the
// pops in reverse push order. Blank lines survive.
func TestWriteBodyReplacesMarkersLineByLine(t *testing.T) {
	body := "\tmov rax, rbx\n\tmov rax, rax\n" + restoreMarker + "\n\tret\n\n\tmov rcx, 1\n" + restoreMarker + "\n\tret\n"
	var b strings.Builder
	writeBody(lineWriter(&b), body, []int{gpIndex("rbx"), gpIndex("r12")})
	// Each teardown is preceded by the rule that brackets it: the frame's
	// state is remembered here and restored after the return, so a rule meant
	// for one epilogue does not describe the block the layout puts next.
	want := "\tmov rax, rbx\n\t.cfi_remember_state\n\tpop r12\n\tpop rbx\n\tret\n\n\tmov rcx, 1\n\t.cfi_remember_state\n\tpop r12\n\tpop rbx\n\tret\n"
	if got := b.String(); got != want {
		t.Fatalf("writeBody =\n%q\nwant\n%q", got, want)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
