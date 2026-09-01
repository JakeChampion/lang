package arm64

import (
	"sort"

	"github.com/jakechampion/lang/internal/native/suggest"
)

// switchMnemonics lists every mnemonic the two `switch mnem` dispatches —
// assembleInsn's and asmVecForm's — handle by name. The SIMD tables and the
// condition-code families are appended by knownMnemonics, and
// TestSuggestListMatchesDispatch extracts the case strings from the source and
// fails when the two drift — the same guard the x86-64 side carries, and the
// reason `movn` being absent from a list while present in the dispatch (#6060)
// is a test failure rather than a worse error message.
var switchMnemonics = []string{
	"mov", "movz", "movk", "movn",
	"add", "sub", "adds", "subs",
	"and", "orr", "eor", "mul", "udiv", "sdiv", "umulh", "smulh", "adc", "sbc",
	"adcs", "sbcs", "ands", "bic", "bics", "orn", "eon",
	"ngc", "ngcs", "madd", "msub",
	"smull", "umull", "smaddl", "umaddl", "smsubl", "umsubl",
	"tst", "mvn", "negs", "extr", "ror",
	"bfi", "bfxil", "ubfiz", "sbfiz",
	"ccmp", "ccmn", "csinc", "csinv", "csneg",
	"cinc", "cinv", "cneg", "csetm", "csel", "cset",
	"cmn", "neg", "clz", "cls", "rbit", "rev", "rev32", "rev16",
	"addv", "smaxv", "sminv", "umaxv", "uminv", "saddlv", "uaddlv",
	"umov", "smov", "movi", "ld1", "st1", "ld1r",
	"dup", "ins", "ext", "tbl", "xtn", "xtn2", "shrn", "shrn2",
	"sshll", "sshll2", "ushll", "ushll2", "sxtl", "sxtl2", "uxtl", "uxtl2",
	"mrs", "msr",
	"fadd", "fsub", "fmul", "fdiv", "fnmul",
	"fmin", "fmax", "fminnm", "fmaxnm",
	"fmadd", "fmsub", "fnmadd", "fnmsub",
	"fcsel", "fccmp", "fneg", "fabs", "fsqrt",
	"frintm", "frintp", "frintz", "frinta", "frintn",
	"fcmp", "fcmpe", "fmov", "fcvt",
	"scvtf", "fcvtzs", "ucvtf", "fcvtzu",
	"lsl", "lsr", "asr",
	"sxtb", "sxth", "sxtw", "uxtb", "uxth",
	"ubfx", "sbfx", "cmp",
	"ldr", "str", "ldrb", "strb", "ldrh", "strh",
	"ldur", "stur", "ldurb", "sturb", "ldurh", "sturh",
	"ldrsb", "ldrsh", "ldrsw", "ldursb", "ldursh", "ldursw",
	"stp", "ldp",
	"ldxr", "ldaxr", "ldxrb", "ldaxrb", "ldxrh", "ldaxrh",
	"stxr", "stlxr", "stxrb", "stlxrb", "stxrh", "stlxrh",
	"ldar", "ldarb", "ldarh", "stlr", "stlrb", "stlrh",
	"dmb", "dsb", "isb",
	"b", "bl", "cbz", "cbnz", "tbz", "tbnz", "br", "blr",
	"ret", "nop", "svc", "brk",
}

// knownMnemonics is every spelling this assembler accepts, sorted, for the
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
	for _, tab := range []map[string]bool{
		keysOf(vecInt3Ops), keysOf(vecLogical3Ops), keysOf(vecCmpZeroOps),
		keysOf(vecInt2MiscOps), keysOf(vecFP3Ops), keysOf(vecFPCmpZeroOps),
		keysOf(vecFP2MiscOps), keysOf(vecShiftImmOps), keysOf(vecAcrossOps),
	} {
		for m := range tab {
			add(m)
		}
	}
	for m := range vecPermuteOps {
		add(m)
	}
	// Conditional branches come in both spellings the dispatch accepts.
	for cc := range condCodes {
		add("b." + cc)
		add("b" + cc)
	}
	sort.Strings(out)
	return out
}()

// keysOf reduces one of the SIMD tables to a name set. They have different
// value types, so this takes the map by a type parameter rather than making
// every table share a struct it does not need.
func keysOf[V any](m map[string]V) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

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
