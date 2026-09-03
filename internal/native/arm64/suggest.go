package arm64

import (
	"sort"

	"github.com/jakechampion/lang/internal/native/arm64tbl"
	"github.com/jakechampion/lang/internal/native/suggest"
)

// knownMnemonics is every spelling this assembler accepts, sorted, for the
// did-you-mean suggestion on the unsupported-instruction error. It is read
// from internal/native/arm64tbl — the by-name families, the Advanced SIMD
// classes, and the conditional branches in both spellings — which is the
// same table the dispatch routes by, so the list cannot fall behind it.
var knownMnemonics = func() []string {
	seen := map[string]bool{}
	var out []string
	add := func(m string) {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	for _, m := range arm64tbl.ScalarMnemonics() {
		add(m)
	}
	for _, tab := range arm64tbl.VecTables {
		for _, o := range tab.Ops {
			add(o.Mnemonic)
		}
	}
	for cc := range condCodes {
		add("b." + cc)
		add("b" + cc)
	}
	sort.Strings(out)
	return out
}()

// suggestMnemonic returns the closest supported mnemonic when the input is a
// near-miss, or "" when nothing is close.
func suggestMnemonic(mnem string) string {
	return suggest.Closest(mnem, knownMnemonics)
}
