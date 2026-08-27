package arm64ssa

import (
	"fmt"
	"strings"
	"testing"
)

// The over-release counter and the probe that reads it have to name the same
// storage, and the detector has to actually run: a probe wired to a symbol
// nothing writes returns a constant zero, and every test asserting
// `__rc_underflow_count() == 0` would pass while detecting nothing. That is a
// worse failure than the missing-helper link error this replaced, so pin both
// halves against the same symbol.
func TestRcUnderflowProbeReadsWhatRcDecCounts(t *testing.T) {
	var dec, probe strings.Builder
	emitRcDecHelper(func(f string, a ...any) { dec.WriteString(fmt.Sprintf(f, a...) + "\n") })
	emitRcUnderflowCountHelper(func(f string, a ...any) { probe.WriteString(fmt.Sprintf(f, a...) + "\n") })

	if !strings.Contains(dec.String(), rcUnderflowSym) {
		t.Errorf("__fern_rc_dec never touches %s, so nothing counts an over-release:\n%s", rcUnderflowSym, dec.String())
	}
	if !strings.Contains(dec.String(), "str w3, ["+"x2]") {
		t.Errorf("__fern_rc_dec loads the counter but never stores it back:\n%s", dec.String())
	}
	if !strings.Contains(probe.String(), rcUnderflowSym) {
		t.Errorf("__fern_rc_underflow_count does not read %s:\n%s", rcUnderflowSym, probe.String())
	}
}

// Releasing an already-zero count must leave the count alone. Decrementing it
// wraps to 0xffffffff, which turns one over-release into an object that is
// never freed for the rest of the program.
func TestRcDecLeavesAZeroCountAlone(t *testing.T) {
	var b strings.Builder
	emitRcDecHelper(func(f string, a ...any) { b.WriteString(fmt.Sprintf(f, a...) + "\n") })
	body := b.String()
	i := strings.Index(body, ".Lssa_rcdec_underflow:")
	if i < 0 {
		t.Fatalf("__fern_rc_dec has no over-release path:\n%s", body)
	}
	if tail := body[i:]; strings.Contains(tail, "stur w1,") {
		t.Errorf("the over-release path writes the refcount back:\n%s", tail)
	}
	if !strings.Contains(body[:i], "cbz w1, .Lssa_rcdec_underflow") {
		t.Errorf("nothing routes a zero count to the over-release path:\n%s", body[:i])
	}
}

// A call with nothing behind it used to go out silently and die in the
// assembler on a mangled label. It is a backend error now, naming the helper.
func TestCallToAnUndefinedLabelIsRejected(t *testing.T) {
	asm := "fn_main:\n\tbl fn___fern_nonesuch\n\tret\n"
	err := checkNoDanglingCalls(asm)
	if err == nil {
		t.Fatal("a call to an undefined label was accepted")
	}
	if !strings.Contains(err.Error(), "fn___fern_nonesuch") {
		t.Errorf("the error does not name the missing helper: %v", err)
	}
}

// Calls to labels the module does define — a user function, a runtime helper,
// a local block label — must not trip it.
func TestCallsToDefinedLabelsAreAccepted(t *testing.T) {
	asm := "fn_main:\n\tbl fn_helper\n\tbl fn___fern_rc_dec\n\tret\n" +
		"fn_helper:\n\tret\n" +
		"fn___fern_rc_dec:\n\tret\n"
	if err := checkNoDanglingCalls(asm); err != nil {
		t.Errorf("a module that defines everything it calls was rejected: %v", err)
	}
}
