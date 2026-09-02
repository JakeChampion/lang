package x86_64

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
)

// TestSuggestListMatchesDispatch pins switchMnemonics to the case strings of
// the insn dispatch switch, extracted from the source, so a mnemonic added to
// one but not the other fails here instead of silently degrading suggestions.
func TestSuggestListMatchesDispatch(t *testing.T) {
	src, err := os.ReadFile("asm.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func (a *Assembler) insn(")
	if start < 0 {
		t.Fatal("insn dispatch not found in asm.go")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("insn dispatch end not found")
	}
	body = body[start : start+end]

	// A case list may span lines, and movdqu/movdqa are dispatched by a
	// `mnem == "..."` comparison rather than a case — collect the string
	// literals of both shapes.
	fromSwitch := map[string]bool{}
	litRe := regexp.MustCompile(`"([a-z0-9]+)"`)
	for _, idx := range regexp.MustCompile(`\bcase\b`).FindAllStringIndex(body, -1) {
		rest := body[idx[1]:]
		if colon := clauseEnd(rest); colon >= 0 {
			for _, lit := range litRe.FindAllStringSubmatch(rest[:colon], -1) {
				fromSwitch[lit[1]] = true
			}
		}
	}
	for _, m := range regexp.MustCompile(`mnem == "([a-z0-9]+)"`).FindAllStringSubmatch(body, -1) {
		fromSwitch[m[1]] = true
	}
	// The two directions are checked against different sets. A mnemonic the
	// dispatch handles may be covered either by the hand-written list or by
	// the no-operand table it looks up, so the forward check uses the union
	// — but a STALE hand-list entry is only detectable against the hand list
	// itself, and the table legitimately carries spellings (cbtw, cdqe) that
	// have no case of their own.
	inList := map[string]bool{}
	for _, m := range switchMnemonics {
		inList[m] = true
	}
	covered := map[string]bool{}
	for m := range inList {
		covered[m] = true
	}
	for m := range fixedOps {
		covered[m] = true
	}
	for m := range fromSwitch {
		if !covered[m] {
			t.Errorf("dispatch handles %q but neither switchMnemonics nor x86tbl.FixedOps covers it — suggestions will not know it", m)
		}
	}
	for m := range inList {
		if !fromSwitch[m] {
			t.Errorf("switchMnemonics lists %q but the dispatch has no such case — remove the stale entry", m)
		}
	}
}

// clauseEnd returns the offset of the first colon outside a string literal —
// the end of a `case` clause whose list may span lines.
func clauseEnd(s string) int {
	inStr := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inStr = !inStr
		case ':':
			if !inStr {
				return i
			}
		case '\n':
			if !inStr && i+1 < len(s) && !strings.HasPrefix(strings.TrimLeft(s[i:], "\n\t "), `"`) {
				return -1 // clause never closed before unrelated code
			}
		}
	}
	return -1
}

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
