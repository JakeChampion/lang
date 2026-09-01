package x86_64

import "sort"

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
// near-miss (one edit for short names, two for longer — enough to catch a
// typo or an AVX spelling like vaddpd), or "" when nothing is close. Ties go
// to the lexicographically first candidate so the message is deterministic.
func suggestMnemonic(mnem string) string {
	if len(mnem) < 2 {
		return ""
	}
	maxDist := 1
	if len(mnem) > 4 {
		maxDist = 2
	}
	best, bestDist := "", maxDist+1
	for _, k := range knownMnemonics {
		if d := editDistance(mnem, k, maxDist); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

// editDistance is the optimal-string-alignment distance (Levenshtein plus
// adjacent transposition), capped: once every entry in a row exceeds max the
// true distance cannot come back under it, so max+1 is returned early.
func editDistance(a, b string, max int) int {
	if len(a)-len(b) > max || len(b)-len(a) > max {
		return max + 1
	}
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d := min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				if t := prev2[j-2] + 1; t < d {
					d = t
				}
			}
			cur[j] = d
			if d < rowMin {
				rowMin = d
			}
		}
		if rowMin > max {
			return max + 1
		}
		prev2, prev, cur = prev, cur, prev2
	}
	if prev[len(b)] > max {
		return max + 1
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
