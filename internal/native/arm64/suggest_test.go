package arm64

import (
	"strings"
	"testing"
)

// TestSuggestKnownMnemonicsAccepted checks the vocabulary is real: every entry
// must be something the assembler actually routes. A suggestion naming a
// mnemonic that then fails to assemble is worse than no suggestion.
//
// A bare mnemonic reaches the scalar dispatch but never the SIMD one, which
// only runs when the first operand is a vN.<arr> register, so the vector
// vocabulary is checked through asmVecClass directly. Both paths are asked only
// whether they RECOGNISE the mnemonic; the operand errors that follow are the
// right answer for an instruction with no operands.
func TestSuggestKnownMnemonicsAccepted(t *testing.T) {
	vecOps := []string{"v0.4s", "v1.4s", "v2.4s"}
	for _, m := range knownMnemonics {
		_, err := Assemble("\t" + m + "\n")
		if err == nil || !strings.Contains(err.Error(), "unsupported instruction") {
			continue
		}
		if handled, _ := asmVecClass(&Assembler{}, m, vecOps); handled {
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
