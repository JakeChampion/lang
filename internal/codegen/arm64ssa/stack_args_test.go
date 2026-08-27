package arm64ssa_test

import (
	"regexp"
	"strconv"
	"testing"

	arm64ssa "github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// sumModule builds `main() = sum(1, 2, ..., n)` where sum takes n parameters
// and adds them all. Past the eight the AArch64 PCS passes in registers, the
// rest travel through the caller's outgoing-argument area.
func sumModule(n int) map[string]*ssa.Func {
	sum := ssa.NewFunc("sum")
	ps := make([]ssa.Value, n)
	for i := range ps {
		ps[i] = sum.AddParam()
	}
	sb := sum.NewBlock()
	acc := ps[0]
	for _, p := range ps[1:] {
		acc = sum.AddOp(sb, ssa.OpAdd, acc, p)
	}
	sum.SetRet(sb, acc)

	main := ssa.NewFunc("main")
	mb := main.NewBlock()
	args := make([]ssa.Value, n)
	for i := range args {
		args[i] = constOp(main, mb, int64(i+1))
	}
	main.SetRet(mb, callOp(main, mb, "sum", args...))
	return map[string]*ssa.Func{"sum": sum, "main": main}
}

// A call with more arguments than the PCS passes in registers used to be a hard
// error out of emit — which is what kept 14 self-host functions and 43 of their
// call sites from compiling at all. The surplus goes on the stack now.
func TestCallPassesSurplusArgumentsOnTheStack(t *testing.T) {
	asm, err := arm64ssa.EmitAsmModule(sumModule(12), "main", arm64ssa.DefaultNumAlloc, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := funcText(t, asm, "main")
	// Four surplus arguments, so four stores into the outgoing area, at the
	// bottom of the frame: [sp, #0], #8, #16, #24.
	for _, off := range []int{0, 8, 16, 24} {
		re := regexp.MustCompile(`\n\t(str|stp) x\d+(, x\d+)?, \[sp, #` + strconv.Itoa(off) + `\]\n`)
		if !re.MatchString(body) {
			t.Errorf("no store into outgoing-argument slot [sp, #%d]:\n%s", off, body)
		}
	}
}

// The callee side: a parameter past the register half is read from the caller's
// frame, which sits above the callee's own.
func TestCalleeReadsItsStackParameters(t *testing.T) {
	asm, err := arm64ssa.EmitAsmModule(sumModule(12), "main", arm64ssa.DefaultNumAlloc, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := funcText(t, asm, "sum")
	frame := regexp.MustCompile(`\n\tsub sp, sp, #(\d+)\n`).FindStringSubmatch(body)
	if frame == nil {
		t.Fatalf("sum has no frame to read its stack parameters above:\n%s", body)
	}
	base, _ := strconv.Atoi(frame[1])
	for k := 0; k < 4; k++ {
		want := base + 8*k
		re := regexp.MustCompile(`\n\tldr x\d+, \[sp, #` + strconv.Itoa(want) + `\]\n`)
		if !re.MatchString(body) {
			t.Errorf("stack parameter %d is not read from [sp, #%d] (frame %d):\n%s", k, want, base, body)
		}
	}
}

// What actually matters: every argument arrives, in the right order, with the
// right value. 12 parameters is four past the register half; 20 is twelve, so
// the area is wider than the register file.
func TestStackArgumentsArriveIntact(t *testing.T) {
	for _, n := range []int{9, 12, 20} {
		// 1 + 2 + ... + n
		moduleMatchesEval(t, sumModule(n), "main")
	}
}

// A function whose arguments all fit in registers must not reserve an
// outgoing-argument area, or every leaf in the program pays for the few calls
// that need one.
func TestNoOutgoingAreaWhenEveryArgumentFits(t *testing.T) {
	small, err := arm64ssa.EmitAsmModule(sumModule(8), "main", arm64ssa.DefaultNumAlloc, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := funcText(t, small, "sum")
	if regexp.MustCompile(`\n\tldr x\d+, \[sp, #\d+\]\n`).MatchString(body) {
		t.Errorf("an 8-parameter callee reads a stack parameter:\n%s", body)
	}
}
