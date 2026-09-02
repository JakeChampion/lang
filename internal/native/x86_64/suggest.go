package x86_64

import (
	"sort"

	"github.com/jakechampion/lang/internal/native/suggest"
	"github.com/jakechampion/lang/internal/native/x86tbl"
)

// switchMnemonics lists every mnemonic the insn dispatch switch handles by
// name. The cc-suffixed families, the sseOps/sse38Ops tables, the GPR groups
// (x86tbl.Groups) and the no-operand vocabulary (x86tbl.FixedOps) are reached
// by lookup rather than by a case and are appended by knownMnemonics.
// TestSuggestListMatchesDispatch extracts the case strings from the source
// and fails when the two drift.
var switchMnemonics = []string{
	"rep", "repe", "repz", "repne", "repnz", "lock",
	"push", "pop", "mov", "movabs",
	"test", "imul", "bsf", "bsr", "lzcnt", "tzcnt", "popcnt",
	"shld", "shrd",
	"bswap", "xchg", "xadd", "cmpxchg",
	"lea", "movzx", "movsx", "movsxd", "jmp", "call",
	"movq", "movd", "movss", "movups", "movupd",
	"cvtsi2sd", "cvtsi2ss", "cvttsd2si", "cvttss2si", "cvtsd2si", "cvtss2si",
	"roundsd", "roundss", "pcmpistri", "pcmpestri",
	"pmovmskb", "movmskps", "movmskpd",
	"pshufd", "shufps", "shufpd",
	"pextrb", "pextrw", "pextrd", "pextrq",
	"pinsrb", "pinsrw", "pinsrd", "pinsrq",
	"crc32",
	"psllw", "psrlw", "psraw", "pslld", "psrld", "psrad",
	"psllq", "psrlq", "pslldq", "psrldq",
	"movdqu", "movdqa",
}

// knownMnemonics is every spelling the assembler accepts, sorted, for the
// did-you-mean suggestion on the unsupported-instruction error.
var knownMnemonics = func() []string {
	seen := map[string]bool{}
	var out []string
	add := func(m string) {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	for _, m := range switchMnemonics {
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
	for m := range sse38Ops {
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

// KnownMnemonics is every spelling this assembler accepts, sorted. It backs
// the did-you-mean suggestion, and the self-host coverage gate reads it as
// the native vocabulary the Fern assembler is pinned against —
// TestSuggestListMatchesDispatch keeps it honest against the dispatch.
func KnownMnemonics() []string {
	return append([]string(nil), knownMnemonics...)
}
