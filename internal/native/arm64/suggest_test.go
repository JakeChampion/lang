package arm64

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// dispatchedMnemonics extracts the case strings of the named `switch mnem`
// dispatches from gas.go's source, so switchMnemonics can be checked against
// what the assembler actually routes.
//
// A case list wraps across lines, so the scan runs to the first colon rather
// than to the end of the line.
func dispatchedMnemonics(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile("gas.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	out := map[string]bool{}
	litRe := regexp.MustCompile(`"([a-z0-9.]+)"`)
	caseRe := regexp.MustCompile(`\bcase\b`)
	for _, fn := range []string{"func assembleInsn(", "func asmVecForm("} {
		start := strings.Index(body, fn)
		if start < 0 {
			t.Fatalf("%s not found in gas.go — the extraction pattern has gone stale, which would make this test vacuous", fn)
		}
		end := strings.Index(body[start:], "\n}\n")
		if end < 0 {
			t.Fatalf("end of %s not found", fn)
		}
		b := body[start : start+end]
		for _, idx := range caseRe.FindAllStringIndex(b, -1) {
			rest := b[idx[1]:]
			colon := strings.Index(rest, ":")
			if colon < 0 {
				continue
			}
			for _, lit := range litRe.FindAllStringSubmatch(rest[:colon], -1) {
				out[lit[1]] = true
			}
		}
	}
	if len(out) < 150 {
		t.Fatalf("extracted only %d mnemonics from the dispatches — the pattern has gone stale", len(out))
	}
	return out
}

// TestSuggestListMatchesDispatch pins switchMnemonics to the case strings of
// the dispatch, so a mnemonic added to one but not the other fails here instead
// of silently degrading suggestions.
//
// The x86-64 side has carried this guard since its suggestions landed. It
// matters more here: #6060 was `movn` present in the dispatch but missing from
// a hand-kept list, and the symptom was a worse error message, which nothing
// fails on.
func TestSuggestListMatchesDispatch(t *testing.T) {
	fromSwitch := dispatchedMnemonics(t)

	listed := map[string]bool{}
	for _, m := range switchMnemonics {
		listed[m] = true
	}
	for m := range fromSwitch {
		if !listed[m] {
			t.Errorf("%q is dispatched but missing from switchMnemonics, so it is not offered as a suggestion", m)
		}
	}
	for m := range listed {
		if !fromSwitch[m] {
			t.Errorf("%q is in switchMnemonics but no longer dispatched, so the suggestion points at nothing", m)
		}
	}
}

// TestSuggestKnownMnemonicsAccepted checks the vocabulary is real: every entry
// must be something the assembler actually routes. A suggestion naming a
// mnemonic that then fails to assemble is worse than no suggestion.
//
// A bare mnemonic reaches the scalar dispatch but never the SIMD one, which
// only runs when the first operand is a vN.<arr> register, so the vector
// vocabulary is checked through asmVecForm directly. Both paths are asked only
// whether they RECOGNISE the mnemonic; the operand errors that follow are the
// right answer for an instruction with no operands.
func TestSuggestKnownMnemonicsAccepted(t *testing.T) {
	vecOps := []string{"v0.4s", "v1.4s", "v2.4s"}
	for _, m := range knownMnemonics {
		_, err := Assemble("\t" + m + "\n")
		if err == nil || !strings.Contains(err.Error(), "unsupported instruction") {
			continue
		}
		if handled, _ := asmVecForm(&Assembler{}, m, vecOps); handled {
			continue
		}
		t.Errorf("%q is offered as a suggestion but is not dispatched: %v", m, err)
	}
}

// TestSuggestOnUnsupported pins the messages a user actually sees.
func TestSuggestOnUnsupported(t *testing.T) {
	cases := []struct{ src, want string }{
		// A typo.
		{"\tmvo x0, x1\n", `did you mean "mov"`},
		// An x86 spelling on the wrong ISA: close to `str`.
		{"\tstr8 x0, [x1]\n", `did you mean "str"`},
		// A transposition in a load-store pair.
		{"\tspt x0, x1, [sp]\n", `did you mean "stp"`},
	}
	for _, c := range cases {
		_, err := Assemble(c.src)
		if err == nil {
			t.Errorf("Assemble(%q): expected an error", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("Assemble(%q) error %q, want it to contain %q", c.src, err, c.want)
		}
	}
	// Nothing close: the error stays bare rather than offering a wild guess.
	_, err := Assemble("\tfrobnicate x0\n")
	if err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("far-off mnemonic got a suggestion: %v", err)
	}
}

// TestNoPanicOnMalformedOperands feeds every mnemonic the wrong number of
// operands. An assembler must reject bad input with an error, never crash: a
// panic here surfaces as a compiler crash with no source location.
//
// It found asmLoadStore reading ops[0] before its arity check, so `ldr` with
// no operands took down the process.
func TestNoPanicOnMalformedOperands(t *testing.T) {
	forms := []string{"", " x0", " x0, x1", " x0, x1, x2, x3", " v0.4s", " [x0]"}
	for _, m := range knownMnemonics {
		for _, f := range forms {
			src := "\t" + m + f + "\n"
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("Assemble(%q) panicked: %v", src, r)
					}
				}()
				_, _ = Assemble(src)
			}()
		}
	}
}
