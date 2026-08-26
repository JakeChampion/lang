package arm64ssa_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	arm64ssa "github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// funcText slices the emitted module down to one function's body: from its
// `fn_<name>:` label to the next label in column 0 that is not a block label.
func funcText(t *testing.T, asm, name string) string {
	t.Helper()
	lines := strings.Split(asm, "\n")
	start := -1
	for i, l := range lines {
		if l == "fn_"+name+":" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("no fn_%s: label in module\n%s", name, asm)
	}
	for i := start + 1; i < len(lines); i++ {
		l := lines[i]
		if strings.HasSuffix(l, ":") && !strings.HasPrefix(l, "\t") && !strings.HasPrefix(l, ".") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}

// calleeSavedMentioned lists the x19..x28 registers named anywhere in body.
func calleeSavedMentioned(body string) []string {
	var out []string
	for r := 19; r <= 28; r++ {
		n := strconv.Itoa(r)
		if regexp.MustCompile(`\b(x` + n + `|w` + n + `)\b`).MatchString(body) {
			out = append(out, "x"+n)
		}
	}
	return out
}

// crossCallModule builds `main() = keep + ident(3)` where `keep` is defined
// before the call and used after it, so it is live across the call. ident is a
// separate function so the call is a real `bl`.
func crossCallModule() map[string]*ssa.Func {
	ident := ssa.NewFunc("ident")
	ip := ident.AddParam()
	ib := ident.NewBlock()
	ident.SetRet(ib, ip)

	main := ssa.NewFunc("main")
	mb := main.NewBlock()
	keep := constOp(main, mb, 39)
	got := callOp(main, mb, "ident", constOp(main, mb, 3))
	main.SetRet(mb, main.AddOp(mb, ssa.OpAdd, keep, got))

	return map[string]*ssa.Func{"ident": ident, "main": main}
}

// A value live across a call is kept in a callee-saved register and preserved
// ONCE by the prologue, instead of being stored and reloaded at the call. That
// is the whole point of mapping part of the allocatable file onto x19..x28: the
// per-call save is what made the SSA backend's code grow on call-dense input.
func TestCallCrossingValueUsesCalleeSavedRegister(t *testing.T) {
	asm, err := arm64ssa.EmitAsmModule(crossCallModule(), "main", arm64ssa.DefaultNumAlloc, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := funcText(t, asm, "main")
	used := calleeSavedMentioned(body)
	if len(used) == 0 {
		t.Fatalf("no callee-saved register used for the call-crossing value:\n%s", body)
	}
	for _, x := range used {
		// Saved in the prologue: before the first block label.
		prologue := body
		if i := strings.Index(body, "\n.L"); i >= 0 {
			prologue = body[:i]
		}
		if !strings.Contains(prologue, "str "+x+", [sp,") {
			t.Errorf("%s is used but not saved in the prologue:\n%s", x, prologue)
		}
		if !strings.Contains(body, "ldr "+x+", [sp,") {
			t.Errorf("%s is used but never restored:\n%s", x, body)
		}
		// The register must not also be preserved at the call: the callee is
		// what guarantees it, so a per-call save would be pure waste. The
		// prologue save is the only `str` of it, so there is exactly one.
		if n := strings.Count(body, "str "+x+", [sp,"); n != 1 {
			t.Errorf("%s stored %d times, want exactly the one prologue save:\n%s", x, n, body)
		}
	}
}

// The same module run end-to-end: the value really does come back intact
// through the callee's own use of the register file.
func TestCallCrossingValueSurvivesAtRuntime(t *testing.T) {
	moduleMatchesEval(t, crossCallModule(), "main") // 39 + 3 = 42
}

// A leaf that fits in the caller-saved half touches no callee-saved register, so
// it pays nothing for the wider file. The allocator's preference is what makes
// this hold: call-crossing values go to callee-saved registers, everything else
// to caller-saved ones.
func TestLeafFunctionTouchesNoCalleeSavedRegisters(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	a := constOp(f, e, 3)
	b := constOp(f, e, 4)
	sum := f.AddOp(e, ssa.OpAdd, a, b)
	f.SetRet(e, f.AddOp(e, ssa.OpMul, sum, constOp(f, e, 5)))

	asm, err := arm64ssa.EmitAsmModule(map[string]*ssa.Func{"main": f}, "main", arm64ssa.DefaultNumAlloc, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := funcText(t, asm, "main")
	if used := calleeSavedMentioned(body); len(used) != 0 {
		t.Errorf("leaf function touches callee-saved %v; it should stay in the caller-saved half:\n%s", used, body)
	}
}

// numAlloc above the mapped file is rejected rather than indexing off the end of
// armX at some unrelated line.
func TestNumAllocBeyondMappedFileIsRejected(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	f.SetRet(e, constOp(f, e, 1))
	_, err := arm64ssa.EmitAsmModule(map[string]*ssa.Func{"main": f}, "main", arm64ssa.DefaultNumAlloc+1, nil)
	if err == nil {
		t.Fatal("numAlloc beyond the mapped allocatable file was accepted")
	}
}
