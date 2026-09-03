package x86_64

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
)

// TestSuggestKnownMnemonicsAccepted probes every spelling in knownMnemonics
// through the assembler under several operand shapes: at least one probe must
// avoid "unsupported instruction" — proving the suggestion list only offers
// mnemonics the dispatch actually takes. (Operand-shape complaints are fine;
// which shape a mnemonic wants is not this test's business.)
func TestSuggestKnownMnemonicsAccepted(t *testing.T) {
	shapes := []string{"", " 0, 0, 0, 0", " xmm0, 1", " rax, rbx"}
	for _, m := range knownMnemonics {
		accepted := false
		for _, s := range shapes {
			_, _, err := AssembleProgram(m+s, elf.TextVAddr)
			if err == nil || !strings.Contains(err.Error(), "unsupported instruction \""+m+"\"") {
				accepted = true
				break
			}
		}
		if !accepted {
			t.Errorf("knownMnemonics offers %q but the dispatch rejects it under every probe shape", m)
		}
	}
}

func TestSuggestOnUnsupported(t *testing.T) {
	cases := []struct{ src, want string }{
		// An AVX spelling suggests its SSE ancestor.
		{"vaddpd xmm0, xmm1", `did you mean "addpd"`},
		// AT&T size suffix on an Intel-dialect mnemonic.
		{"movq2 rax, rbx", `did you mean "movq"`},
		// Plain typo.
		{"mvo rax, rbx", `did you mean "mov"`},
	}
	for _, c := range cases {
		_, _, err := AssembleProgram(c.src, elf.TextVAddr)
		if err == nil {
			t.Errorf("AssembleProgram(%q) unexpectedly succeeded", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("AssembleProgram(%q) error %q, want it to contain %q", c.src, err, c.want)
		}
	}
	// Nothing close: the error stays bare rather than offering a wild guess.
	_, _, err := AssembleProgram("frobnicate rax", elf.TextVAddr)
	if err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("far-off mnemonic got a suggestion: %v", err)
	}
}
