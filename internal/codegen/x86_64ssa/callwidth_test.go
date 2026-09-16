package x86_64ssa

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// A 32-bit call result is sign-extended as it is taken out of eax, in the
// one instruction the move already cost, both when the result goes
// straight to its home and when a value live across the call makes the
// capture go through a scratch register first. Nothing re-extends the
// result afterwards, and the value survives a negative return.
func TestCallResultIsSignExtendedOnCapture(t *testing.T) {
	neg := ssa.NewFunc("neg")
	nx := neg.AddParam()
	ne := neg.NewBlock()
	neg.SetRet(ne, neg.AddOp(ne, ssa.OpSub, constOp(neg, ne, 0), nx))

	// direct: nothing is live across the call, so the result is captured
	// into its home; across: x is read after both calls, so the second
	// call's result is staged through the scratch register.
	main := ssa.NewFunc("main")
	x := main.AddParam()
	me := main.NewBlock()
	c1 := callOp(main, me, "neg", x)
	me.Ops[len(me.Ops)-1].Width = 32
	c2 := callOp(main, me, "neg", c1)
	me.Ops[len(me.Ops)-1].Width = 32
	main.SetRet(me, main.AddOp(me, ssa.OpAdd, main.AddOp(me, ssa.OpAdd, c1, c2), x))
	funcs := map[string]*ssa.Func{"neg": neg, "main": main}

	asm, err := EmitAsmModule(funcs, "main", 8, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := funcText(t, asm, fnLabel("main"))
	body = body[strings.Index(body, ".L"):]
	self := regexp.MustCompile(`movsxd (\w+), (\w+)d\n`)
	for _, m := range self.FindAllStringSubmatch(body, -1) {
		if reg32n(regIndex(t, m[1])) == m[2] {
			t.Errorf("a call result is re-extended in place after being moved:\n%s", body)
			break
		}
	}
	// The line after each call is the capture: a movsxd from eax, never a
	// plain move.
	lines := strings.Split(body, "\n")
	captures := 0
	for i, ln := range lines {
		if !strings.HasPrefix(strings.TrimSpace(ln), "call ") || i+1 >= len(lines) {
			continue
		}
		next := strings.TrimSpace(lines[i+1])
		if regexp.MustCompile(`^movsxd \w+, eax$`).MatchString(next) {
			captures++
		} else {
			t.Errorf("a 32-bit call result is taken out of rax with %q; want a movsxd from eax:\n%s", next, body)
		}
	}
	if captures != 2 {
		t.Errorf("main captures %d call results; want 2", captures)
	}
	for _, nAlloc := range []int{1, 2, 8} {
		runModuleMatchesEval(t, funcs, "main", nAlloc, []int64{7}) // -7 + 7 + 7
	}
}

// regIndex maps a 64-bit register name back to its gpRegs index.
func regIndex(t *testing.T, name string) int {
	t.Helper()
	for i, r := range gpRegs {
		if r == name {
			return i
		}
	}
	t.Fatalf("%q is not an allocatable register", name)
	return -1
}
