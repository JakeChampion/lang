package arm64ssa_test

import (
	"regexp"
	"strings"
	"testing"

	arm64ssa "github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// manyLiveAcrossCall builds `main() = (v1 + v2 + ... + vN) + ident(1)` with the
// vs all defined before the call and used after it. N is chosen above the ten
// callee-saved registers, so the surplus lands in caller-saved ones and has to
// be preserved at the call itself — the call-save area this exercises.
func manyLiveAcrossCall(n int) map[string]*ssa.Func {
	ident := ssa.NewFunc("ident")
	ip := ident.AddParam()
	ib := ident.NewBlock()
	ident.SetRet(ib, ip)

	main := ssa.NewFunc("main")
	mb := main.NewBlock()
	keep := make([]ssa.Value, n)
	for i := range keep {
		keep[i] = constOp(main, mb, int64(i+1))
	}
	sum := callOp(main, mb, "ident", constOp(main, mb, 1))
	for _, v := range keep {
		sum = main.AddOp(mb, ssa.OpAdd, sum, v)
	}
	main.SetRet(mb, sum)
	return map[string]*ssa.Func{"ident": ident, "main": main}
}

var (
	strSlot = regexp.MustCompile(`(?m)^\tstr x\d+, \[sp, #\d+\]$`)
	ldrSlot = regexp.MustCompile(`(?m)^\tldr x\d+, \[sp, #\d+\]$`)
	stpSlot = regexp.MustCompile(`(?m)^\tstp x\d+, x\d+, \[sp, #\d+\]$`)
	ldpSlot = regexp.MustCompile(`(?m)^\tldp x\d+, x\d+, \[sp, #\d+\]$`)
)

// The call-save area is contiguous 8-byte slots, so two registers fit one
// stp/ldp. It is the largest block of memory traffic the backend emits — 6.4
// ops at every call on compiler-shaped input — so the saves must go out paired,
// not one instruction per register.
func TestCallSaveAreaPairsItsSlots(t *testing.T) {
	asm, err := arm64ssa.EmitAsmModule(manyLiveAcrossCall(14), "main", arm64ssa.DefaultNumAlloc, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := funcText(t, asm, "main")
	pairs := len(stpSlot.FindAllString(body, -1))
	if pairs == 0 {
		t.Fatalf("no stp in a function with more caller-saved values live across a call than fit:\n%s", body)
	}
	if got := len(ldpSlot.FindAllString(body, -1)); got != pairs {
		t.Errorf("%d stp against %d ldp; every paired save needs its paired restore:\n%s", pairs, got, body)
	}
	// Singles are the odd tail only: at most one store and one load may be left
	// over, plus the x30 save a calling function always makes.
	if got := len(strSlot.FindAllString(body, -1)); got > 2 {
		t.Errorf("%d unpaired str, want the x30 save plus at most one odd tail:\n%s", got, body)
	}
	if got := len(ldrSlot.FindAllString(body, -1)); got > 2 {
		t.Errorf("%d unpaired ldr, want the x30 restore plus at most one odd tail:\n%s", got, body)
	}
}

// The prologue's callee-saved saves use the same contiguous area and pair the
// same way.
func TestProloguePairsItsCalleeSavedSlots(t *testing.T) {
	asm, err := arm64ssa.EmitAsmModule(manyLiveAcrossCall(14), "main", arm64ssa.DefaultNumAlloc, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := funcText(t, asm, "main")
	prologue := body
	if i := strings.Index(body, "\n.L"); i >= 0 {
		prologue = body[:i]
	}
	if len(calleeSavedMentioned(body)) < 2 {
		t.Fatalf("expected at least two callee-saved registers in play:\n%s", body)
	}
	if !stpSlot.MatchString(prologue) {
		t.Errorf("prologue saves its callee-saved registers one at a time:\n%s", prologue)
	}
}

// Pairing rewrites how every live-across value is preserved, so the value that
// matters is whether they all come back.
func TestPairedCallSaveAreaRoundTrips(t *testing.T) {
	// 1 (from ident) + 1 + 2 + ... + 14 = 106.
	moduleMatchesEval(t, manyLiveAcrossCall(14), "main")
}
