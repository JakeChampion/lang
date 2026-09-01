package x86_64

import (
	"sort"

	"github.com/jakechampion/lang/internal/native/suggest"
)

// switchMnemonics lists every mnemonic the insn dispatch switch handles by
// name (the cc-suffixed families and the sseOps/sse38Ops tables are appended
// by knownMnemonics). TestSuggestListMatchesDispatch extracts the case
// strings from the source and fails when the two drift.
var switchMnemonics = []string{
	"rep", "repe", "repz", "repne", "repnz", "lock",
	"ret", "syscall", "ud2", "nop", "int3", "leave", "pause",
	"mfence", "lfence", "sfence",
	"cbw", "cwde", "cdqe", "cwd", "pushfq", "popfq", "cdq", "cqo",
	"cld", "std",
	"movsb", "movsw", "movsd", "movsq",
	"stosb", "stosw", "stosd", "stosq",
	"cmpsb", "cmpsw", "cmpsd", "cmpsq",
	"scasb", "scasw", "scasd", "scasq",
	"lodsb", "lodsw", "lodsd", "lodsq",
	"push", "pop", "mov", "movabs",
	"add", "or", "adc", "sbb", "and", "sub", "xor", "cmp",
	"test", "imul", "bsf", "bsr", "lzcnt", "tzcnt", "popcnt",
	"idiv", "div", "mul", "neg", "not", "inc", "dec",
	"sar", "shl", "shr", "rol", "ror", "rcl", "rcr", "shld", "shrd",
	"bt", "bts", "btr", "btc", "bswap", "xchg", "xadd", "cmpxchg",
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
