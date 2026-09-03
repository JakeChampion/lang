package x86_64

import (
	"sort"

	"github.com/jakechampion/lang/internal/native/suggest"
	"github.com/jakechampion/lang/internal/native/x86tbl"
)

// knownMnemonics is every spelling the assembler accepts, sorted, for the
// did-you-mean suggestion on the unsupported-instruction error. It is read
// from internal/native/x86tbl — the by-name families, the no-operand
// vocabulary, the groups, the SSE table and the condition families —
// which is the same table the dispatch routes by, so the list cannot fall
// behind it.
var knownMnemonics = func() []string {
	seen := map[string]bool{}
	var out []string
	add := func(m string) {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	for _, m := range x86tbl.NamedIntelMnemonics() {
		add(m)
	}
	for m := range fixedOps {
		add(m)
	}
	for _, g := range x86tbl.Groups {
		for _, m := range g.Spellings() {
			add(m)
		}
	}
	for m := range sseOps {
		add(m)
	}
	for cc := range condCodes {
		add("j" + cc)
		add("set" + cc)
		add("cmov" + cc)
	}
	sort.Strings(out)
	return out
}()

// suggestMnemonic returns the closest supported mnemonic when the input is a
// near-miss, or "" when nothing is close.
func suggestMnemonic(mnem string) string {
	return suggest.Closest(mnem, knownMnemonics)
}
