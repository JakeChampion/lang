package e2eselfhost

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
	"github.com/jakechampion/lang/internal/native/x86_64"
	"github.com/jakechampion/lang/internal/native/x86tbl"
)

// Coverage parity between the self-host x86-64 assembler and the native (Go)
// assembler that serves as its oracle, at Go speed: the mnemonic set the
// self-host dispatch accepts is read out of the Fern source as data (the
// same pattern as internal/caps/selfhost_parity_test.go) and every entry is
// probed against the native assembler with a representative instruction.
//
// The rule this test enforces (#6075): teach the oracle first. A mnemonic
// the self-host emits or accepts that the native assembler cannot encode is
// a differential that can never run, and the two lists have already drifted
// in both directions — movn was listed with no branch to assemble it
// (#6060), and the unary FP family had encoders but was missing from the
// list (#6044). Nothing here builds the self-host compiler; the test is
// pure text extraction plus native-assembler probes.
//
// The arm64 side no longer needs this: its vocabulary is one table
// (internal/native/arm64tbl) both assemblers are built from, and
// TestSelfHostArm64TableRowsMatchNative assembles every row through both.

// fernFnBody extracts the body of `function NAME(...)` from Fern source,
// with // comments stripped so a mnemonic quoted in prose doesn't read as a
// dispatch case.
func fernFnBody(t *testing.T, src, path, name string) string {
	t.Helper()
	start := strings.Index(src, "function "+name+"(")
	if start < 0 {
		t.Fatalf("no `function %s(` found in %s — the extraction pattern has gone stale, which would make this test vacuous", name, path)
	}
	body := src[start:]
	if end := strings.Index(body[1:], "\nfunction "); end >= 0 {
		body = body[:end+1]
	}
	return regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(body, "")
}

// mnemonicCases pulls the string literals a dispatch compares a variable
// against (`mnem == "add"`, `cmv == "cmovl"`), deduplicated and sorted.
func mnemonicCases(body string, vars ...string) []string {
	seen := map[string]bool{}
	for _, v := range vars {
		re := regexp.MustCompile(v + `\s*==\s*"([^"]+)"`)
		for _, m := range re.FindAllStringSubmatch(body, -1) {
			seen[m[1]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// probeMnemonics checks a mnemonic list against a probe table: every
// extracted mnemonic must have a probe (so the table cannot silently fall
// behind the .fern source), every probe must still correspond to an
// extracted mnemonic (so retired mnemonics take their probe with them), and
// every probe must assemble with the native oracle.
func probeMnemonics(t *testing.T, path string, mnemonics []string, probes map[string]string, assemble func(string) error) {
	t.Helper()
	if len(mnemonics) == 0 {
		t.Fatalf("no mnemonics extracted from %s — the extraction pattern has gone stale, which would make this test vacuous", path)
	}
	listed := map[string]bool{}
	for _, m := range mnemonics {
		listed[m] = true
		probe, ok := probes[m]
		if !ok {
			t.Errorf("%s dispatches %q but this test has no probe for it — add a probe so the native oracle keeps covering it", path, m)
			continue
		}
		if err := assemble(probe); err != nil {
			t.Errorf("self-host mnemonic %q: the native assembler rejects the probe %q: %v\n(teach the oracle first — the self-host must not accept instructions the native assembler cannot cross-check)", m, probe, err)
		}
	}
	for m := range probes {
		if !listed[m] {
			t.Errorf("probe table lists %q, which %s no longer dispatches — remove the stale probe", m, path)
		}
	}
}

// x86KnownHelpers are the lookup helpers x86_gas_emit consults instead of
// spelling those mnemonics as literals in its own body — the suffix
// families' base-mnemonic tables, the no-operand byte table and its
// rep-eligibility predicate, and the SSE tables (#7893). Their bodies are extracted alongside x86_gas_emit's
// so every mnemonic the assembler accepts is enumerated; a helper that
// disappears fails loudly in fernFnBody rather than silently shrinking
// the probed surface.
var x86KnownHelpers = []string{
	"x86_gas_alu_ext", "x86_gas_unary_ext", "x86_gas_incdec_ext",
	"x86_gas_shift_ext", "x86_gas_bt_idx",
	"x86_gas_fixed_op", "x86_gas_rep_ok",
	"x86_gas_sse_fp_op", "x86_gas_sse_int_op", "x86_gas_sse38_op",
	"x86_gas_vshift_op", "x86_gas_imm3a_op", "x86_gas_shuf_op",
	"x86_gas_cvt2si", "x86_gas_extend_op",
}

// TestSelfHostAsmCoverageX86_64 pins the mnemonic set the self-host x86-64
// assembler dispatches on (x86_gas_emit in x86_native.fern plus the lookup
// helpers it consults, AT&T dialect) to the native Intel-syntax assembler:
// each AT&T mnemonic, translated to its Intel equivalent, must assemble
// through internal/native/x86_64.
//
// The probe line IS the AT&T-to-Intel mapping table: suffix-family bases
// (add/test/shl/...) probe suffix-less because Intel syntax carries the
// width on the operands; the renamed mnemonics are movabsq to movabs,
// movslq to movsxd, movzbq/movzbl/movzwq to movzx, and the l-suffixed
// string ops to their Intel d spellings (movsl = movsd, ...).
//
// The sign-extend group and pushf/popf probe as THEMSELVES rather than
// translating: gas accepts both dialects' spellings of those in both syntax
// modes, so since #7903 phase 3 both assemblers do too. The rep
// prefix is dispatched on "rep" with the string op matched inside that
// branch, so its probe covers the whole accepted set. cmovcc is
// dispatched by pattern (a condition table plus an optional width
// suffix), so it is pinned by the explicit probe loop at the end rather
// than by extraction.

// x86ConditionSpellings is every condition suffix the x86-64 condition table
// accepts, aliases included — read from the shared table both assemblers are
// built from (#7903), never listed again here. A gate that keeps its own copy
// of the vocabulary it is checking cannot notice the vocabulary changing.
var x86ConditionSpellings = x86tbl.CondSpellings()

func TestSelfHostAsmCoverageX86_64(t *testing.T) {
	const path = "../../examples/self_host/x86_native.fern"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	body := fernFnBody(t, string(b), path, "x86_gas_emit")
	for _, helper := range x86KnownHelpers {
		body += "\n" + fernFnBody(t, string(b), path, helper)
	}
	// `bse`/`bse2` hold the suffix-stripped base mnemonic; their
	// comparisons are dispatch cases too.
	mnemonics := mnemonicCases(body, "mnem", "bse", "bse2")

	probes := map[string]string{
		"ret":       "ret",
		"syscall":   "syscall",
		"leave":     "leave",
		"cqto":      "cqto",
		"cld":       "cld",
		"std":       "std",
		"nop":       "nop",
		"int3":      "int3",
		"pause":     "pause",
		"mfence":    "mfence",
		"lfence":    "lfence",
		"sfence":    "sfence",
		"cmpsd":     "cmpsd",
		"cbw":       "cbw",
		"cwde":      "cwde",
		"cdqe":      "cdqe",
		"cwd":       "cwd",
		"cdq":       "cdq",
		"cqo":       "cqo",
		"cbtw":      "cbtw",
		"cwtl":      "cwtl",
		"cltq":      "cltq",
		"cwtd":      "cwtd",
		"cltd":      "cltd",
		"rep":       "rep movsb\nrep movsq\nrep stosb\nrep stosq",
		"lock":      "lock add qword ptr [rdi], 1",
		"movsb":     "movsb",
		"movsw":     "movsw",
		"movsl":     "movsd",
		"movsq":     "movsq",
		"stosb":     "stosb",
		"stosw":     "stosw",
		"stosl":     "stosd",
		"stosq":     "stosq",
		"lodsb":     "lodsb",
		"lodsw":     "lodsw",
		"lodsl":     "lodsd",
		"lodsq":     "lodsq",
		"scasb":     "scasb",
		"scasw":     "scasw",
		"scasl":     "scasd",
		"scasq":     "scasq",
		"cmpsb":     "cmpsb",
		"cmpsw":     "cmpsw",
		"cmpsl":     "cmpsd",
		"cmpsq":     "cmpsq",
		"call":      "call rax",
		"jmp":       "jmp rdx",
		"pushq":     "push rbp",
		"popq":      "pop rbp",
		"leaq":      "lea rax, [rbp-8]",
		"leal":      "lea eax, [rbp-8]",
		"leaw":      "lea ax, [rbp-8]",
		"movq":      "mov rax, rcx",
		"movabsq":   "movabs rax, 4294967296",
		"movabs":    "movabs rax, 4294967296",
		"movl":      "mov eax, ecx",
		"movw":      "mov ax, cx",
		"movb":      "mov byte ptr [rax], cl",
		"movzbq":    "movzx rax, byte ptr [rdi]",
		"movzbl":    "movzx eax, byte ptr [rdi]",
		"movzwq":    "movzx rax, word ptr [rdi]",
		"movzwl":    "movzx eax, word ptr [rdi]",
		"movzbw":    "movzx ax, byte ptr [rdi]",
		"movslq":    "movsxd rax, ecx",
		"movsxd":    "movsxd rax, ecx",
		"movd":      "movd xmm0, eax",
		"add":       "add rax, rcx",
		"or":        "or rax, rcx",
		"adc":       "adc rax, rcx",
		"sbb":       "sbb rax, rcx",
		"and":       "and rax, rcx",
		"sub":       "sub rax, rcx",
		"xor":       "xor rax, rcx",
		"cmp":       "cmp rax, rcx",
		"test":      "test rax, rcx",
		"not":       "not rax",
		"neg":       "neg rax",
		"mul":       "mul rcx",
		"imul":      "imul rcx",
		"div":       "div rcx",
		"idiv":      "idiv rcx",
		"inc":       "inc rax",
		"dec":       "dec rax",
		"rol":       "rol rax, 3",
		"ror":       "ror rax, 3",
		"rcl":       "rcl rax, 3",
		"rcr":       "rcr rax, 3",
		"shl":       "shl rax, 3",
		"sal":       "sal rax, 3",
		"shr":       "shr rax, cl",
		"sar":       "sar rax, 63",
		"shld":      "shld rsi, rdi, cl",
		"shrd":      "shrd rsi, rdi, 5",
		"bt":        "bt rax, rcx",
		"bts":       "bts rax, 3",
		"btr":       "btr rax, rcx",
		"btc":       "btc rax, 3",
		"bswap":     "bswap rax",
		"xadd":      "xadd rax, rcx",
		"cmpxchg":   "cmpxchg qword ptr [rdi], rcx",
		"xchg":      "xchg qword ptr [rdi], rcx",
		"crc32":     "crc32 eax, cl",
		"bsfl":      "bsf eax, ecx",
		"bsrl":      "bsr eax, ecx",
		"lzcntl":    "lzcnt eax, ecx",
		"lzcntq":    "lzcnt rax, rcx",
		"tzcntl":    "tzcnt eax, ecx",
		"tzcntq":    "tzcnt rax, rcx",
		"popcntl":   "popcnt eax, ecx",
		"popcntq":   "popcnt rax, rcx",
		"cvttsd2si": "cvttsd2si eax, xmm1",
		// #8020: the rest of the scalar convert family. The AT&T l/q suffix
		// names the width Intel reads off the operands, so both spellings
		// probe the same Intel line.
		"cvttsd2sil": "cvttsd2si eax, xmm1",
		"cvttsd2siq": "cvttsd2si rax, xmm1",
		"cvtsd2si":   "cvtsd2si eax, xmm1",
		"cvtsd2sil":  "cvtsd2si eax, xmm1",
		"cvtsd2siq":  "cvtsd2si rax, xmm1",
		"cvtss2si":   "cvtss2si eax, xmm1",
		"cvtss2sil":  "cvtss2si eax, xmm1",
		"cvtss2siq":  "cvtss2si rax, xmm1",
		"cvttss2si":  "cvttss2si eax, xmm1",
		"cvttss2sil": "cvttss2si eax, xmm1",
		"cvttss2siq": "cvttss2si rax, xmm1",
		"cvtsi2ss":   "cvtsi2ss xmm1, rax",
		"cvtsi2ssl":  "cvtsi2ss xmm1, eax",
		"cvtsi2ssq":  "cvtsi2ss xmm1, rax",
		"movss":      "movss xmm1, xmm2",
		"movsbl":     "movsx ecx, al",
		"movsbq":     "movsx rcx, al",
		"movswl":     "movsx ecx, ax",
		"movsbw":     "movsx cx, al",
		"movswq":     "movsx rcx, ax",
		"pushfq":     "pushfq",
		"pushf":      "pushf",
		"popfq":      "popfq",
		"popf":       "popf",
		"ud2":        "ud2",
		"repe":       "repe cmpsb",
		"repz":       "repz cmpsb",
		"repne":      "repne scasb",
		"repnz":      "repnz scasb",
		"cvtsi2sd":   "cvtsi2sd xmm0, rax",
		"cvtsi2sdq":  "cvtsi2sd xmm0, rax",
		"cvtsi2sdl":  "cvtsi2sd xmm0, eax",
		"movdqu":     "movdqu xmm0, [rdi]",
		"movdqa":     "movdqa [rdi], xmm0",
		"movsd":      "movsd xmm0, qword ptr [rdi]",
		"movups":     "movups xmm0, [rdi]",
		"movupd":     "movupd [rdi], xmm0",
		"pmovmskb":   "pmovmskb eax, xmm0",
		"movmskps":   "movmskps eax, xmm0",
		"movmskpd":   "movmskpd eax, xmm0",
		"roundsd":    "roundsd xmm0, xmm1, 0",
		"roundss":    "roundss xmm0, xmm1, 0",
		"pcmpistri":  "pcmpistri xmm0, xmm1, 0",
		"pcmpestri":  "pcmpestri xmm0, xmm1, 0",
		"pshufd":     "pshufd xmm1, xmm2, 0",
		"shufps":     "shufps xmm1, xmm2, 0",
		"shufpd":     "shufpd xmm1, xmm2, 1",
		"pextrb":     "pextrb eax, xmm1, 0",
		"pextrw":     "pextrw eax, xmm1, 0",
		"pextrd":     "pextrd eax, xmm1, 0",
		"pextrq":     "pextrq rax, xmm1, 0",
		"pinsrb":     "pinsrb xmm1, eax, 0",
		"pinsrw":     "pinsrw xmm1, eax, 0",
		"pinsrd":     "pinsrd xmm1, eax, 0",
		"pinsrq":     "pinsrq xmm1, rax, 0",
		"addsd":      "addsd xmm0, xmm1",
		"subsd":      "subsd xmm0, xmm1",
		"mulsd":      "mulsd xmm0, xmm1",
		"divsd":      "divsd xmm0, xmm1",
		"sqrtsd":     "sqrtsd xmm0, xmm1",
		"minsd":      "minsd xmm0, xmm1",
		"maxsd":      "maxsd xmm0, xmm1",
		"addss":      "addss xmm0, xmm1",
		"subss":      "subss xmm0, xmm1",
		"mulss":      "mulss xmm0, xmm1",
		"divss":      "divss xmm0, xmm1",
		"sqrtss":     "sqrtss xmm0, xmm1",
		"minss":      "minss xmm0, xmm1",
		"maxss":      "maxss xmm0, xmm1",
		"ucomisd":    "ucomisd xmm0, xmm1",
		"comisd":     "comisd xmm0, xmm1",
		"ucomiss":    "ucomiss xmm0, xmm1",
		"comiss":     "comiss xmm0, xmm1",
		"cvtss2sd":   "cvtss2sd xmm0, xmm1",
		"cvtsd2ss":   "cvtsd2ss xmm0, xmm1",
		"movapd":     "movapd xmm0, xmm1",
		"movaps":     "movaps xmm0, xmm1",
		"xorpd":      "xorpd xmm0, xmm1",
		"xorps":      "xorps xmm0, xmm1",
		"andpd":      "andpd xmm0, xmm1",
		"andps":      "andps xmm0, xmm1",
		"andnpd":     "andnpd xmm0, xmm1",
		"andnps":     "andnps xmm0, xmm1",
		"orpd":       "orpd xmm0, xmm1",
		"orps":       "orps xmm0, xmm1",
		"addpd":      "addpd xmm0, xmm1",
		"subpd":      "subpd xmm0, xmm1",
		"mulpd":      "mulpd xmm0, xmm1",
		"divpd":      "divpd xmm0, xmm1",
		"sqrtpd":     "sqrtpd xmm0, xmm1",
		"minpd":      "minpd xmm0, xmm1",
		"maxpd":      "maxpd xmm0, xmm1",
		"addps":      "addps xmm0, xmm1",
		"subps":      "subps xmm0, xmm1",
		"mulps":      "mulps xmm0, xmm1",
		"divps":      "divps xmm0, xmm1",
		"sqrtps":     "sqrtps xmm0, xmm1",
		"minps":      "minps xmm0, xmm1",
		"maxps":      "maxps xmm0, xmm1",
		"unpcklpd":   "unpcklpd xmm0, xmm1",
		"unpckhpd":   "unpckhpd xmm0, xmm1",
		"cvtdq2ps":   "cvtdq2ps xmm0, xmm1",
		"cvtps2dq":   "cvtps2dq xmm0, xmm1",
		"cvttps2dq":  "cvttps2dq xmm0, xmm1",
		"cvtdq2pd":   "cvtdq2pd xmm0, xmm1",
		"cvtpd2dq":   "cvtpd2dq xmm0, xmm1",
		"cvttpd2dq":  "cvttpd2dq xmm0, xmm1",
		"pcmpeqb":    "pcmpeqb xmm0, xmm1",
		"pcmpeqw":    "pcmpeqw xmm0, xmm1",
		"pcmpeqd":    "pcmpeqd xmm0, xmm1",
		"pcmpgtb":    "pcmpgtb xmm0, xmm1",
		"pcmpgtw":    "pcmpgtw xmm0, xmm1",
		"pcmpgtd":    "pcmpgtd xmm0, xmm1",
		"punpcklbw":  "punpcklbw xmm0, xmm1",
		"punpcklwd":  "punpcklwd xmm0, xmm1",
		"punpckldq":  "punpckldq xmm0, xmm1",
		"punpcklqdq": "punpcklqdq xmm0, xmm1",
		"punpckhbw":  "punpckhbw xmm0, xmm1",
		"punpckhwd":  "punpckhwd xmm0, xmm1",
		"punpckhdq":  "punpckhdq xmm0, xmm1",
		"punpckhqdq": "punpckhqdq xmm0, xmm1",
		"por":        "por xmm0, xmm1",
		"pand":       "pand xmm0, xmm1",
		"pxor":       "pxor xmm0, xmm1",
		"pandn":      "pandn xmm0, xmm1",
		"paddb":      "paddb xmm0, xmm1",
		"paddw":      "paddw xmm0, xmm1",
		"paddd":      "paddd xmm0, xmm1",
		"paddq":      "paddq xmm0, xmm1",
		"psubb":      "psubb xmm0, xmm1",
		"psubw":      "psubw xmm0, xmm1",
		"psubd":      "psubd xmm0, xmm1",
		"psubq":      "psubq xmm0, xmm1",
		"paddusb":    "paddusb xmm0, xmm1",
		"psubusb":    "psubusb xmm0, xmm1",
		"paddsb":     "paddsb xmm0, xmm1",
		"psubsb":     "psubsb xmm0, xmm1",
		"pavgb":      "pavgb xmm0, xmm1",
		"pminub":     "pminub xmm0, xmm1",
		"pmaxub":     "pmaxub xmm0, xmm1",
		"pminsw":     "pminsw xmm0, xmm1",
		"pmaxsw":     "pmaxsw xmm0, xmm1",
		"pmullw":     "pmullw xmm0, xmm1",
		"pmulhw":     "pmulhw xmm0, xmm1",
		"pmulhuw":    "pmulhuw xmm0, xmm1",
		"pmuludq":    "pmuludq xmm0, xmm1",
		"psadbw":     "psadbw xmm0, xmm1",
		"packsswb":   "packsswb xmm0, xmm1",
		"packuswb":   "packuswb xmm0, xmm1",
		"packssdw":   "packssdw xmm0, xmm1",
		"psllw":      "psllw xmm0, xmm1",
		"pslld":      "pslld xmm0, xmm1",
		"psllq":      "psllq xmm0, xmm1",
		"psrlw":      "psrlw xmm0, xmm1",
		"psrld":      "psrld xmm0, xmm1",
		"psrlq":      "psrlq xmm0, xmm1",
		"psraw":      "psraw xmm0, xmm1",
		"psrad":      "psrad xmm0, xmm1",
		"ptest":      "ptest xmm0, xmm1",
		"pmulld":     "pmulld xmm0, xmm1",
		"pminsb":     "pminsb xmm0, xmm1",
		"pminsd":     "pminsd xmm0, xmm1",
		"pminuw":     "pminuw xmm0, xmm1",
		"pminud":     "pminud xmm0, xmm1",
		"pmaxsb":     "pmaxsb xmm0, xmm1",
		"pmaxsd":     "pmaxsd xmm0, xmm1",
		"pmaxuw":     "pmaxuw xmm0, xmm1",
		"pmaxud":     "pmaxud xmm0, xmm1",
		"pslldq":     "pslldq xmm0, 8",
		"psrldq":     "psrldq xmm0, 8",
	}
	assemble := func(probe string) error {
		_, _, err := x86_64.AssembleProgram(probe+"\n", elf.TextVAddr)
		return err
	}
	probeMnemonics(t, path, mnemonics, probes, assemble)

	// The three condition families x86_gas_emit dispatches by PATTERN, off
	// the one shared table (x86_gas_cc_code). They have no literal for
	// mnemonicCases to extract, so this loop is the only thing that names
	// them — and it names every spelling the table knows, aliases included,
	// which is what makes the reverse-direction exclusion below honest.
	//
	// Acceptance by the native assembler is all this proves. The bytes are
	// pinned in TestSelfHostX86ConditionSpellingsGas, which assembles the
	// same instructions through BOTH assemblers and compares.
	for _, cond := range x86ConditionSpellings {
		for _, probe := range []string{
			"j" + cond + " l0\nl0:\nret",
			"set" + cond + " cl",
			"cmov" + cond + " rax, rcx",
			"cmov" + cond + " eax, ecx",
		} {
			if err := assemble(probe); err != nil {
				t.Errorf("condition-family probe %q: native assembler rejects it: %v", probe, err)
			}
		}
	}

	// AT&T overloads `movq`, and x86_gas_movq handles both meanings — the
	// suffixed general-register move and the SSE one — disambiguating on
	// whether an operand is an xmm register. The probe table is keyed by
	// mnemonic, so it can carry only one of them, and it carries the GPR half
	// (as `mov rax, rcx`). The SSE half is the one whose Intel spelling is
	// also `movq`, so it is the half that pins against the native mnemonic;
	// without these it was unprobed (#8020).
	for _, probe := range []string{"movq xmm0, rax", "movq rax, xmm0", "movq xmm0, xmm1"} {
		if err := assemble(probe); err != nil {
			t.Errorf("movq SSE probe %q: native assembler rejects it: %v", probe, err)
		}
	}

	// The reverse direction (#8020), the twin of the arm64 pin in
	// TestSelfHostArm64TableRowsMatchNative. Everything above pins the self-host to the
	// native assembler; this pins the native assembler to the self-host,
	// which is the direction that had no gate.
	//
	// No exception list, for the same reason as arm64's: one would re-create
	// the hole in a new shape. The condition-code families are excluded by
	// PATTERN rather than by name because both assemblers match them that way
	// and have no literal to compare — the loop above walks every spelling
	// the shared table knows, so nothing in these families goes unnamed.
	//
	// The pattern is built FROM that same list, so a spelling cannot be
	// excluded here without also being probed there.
	condFamily := regexp.MustCompile(`^(cmov|j|set)(` + strings.Join(x86ConditionSpellings, "|") + `)$`)
	reached := map[string]bool{}
	for _, probe := range probes {
		if f := strings.Fields(probe); len(f) > 0 {
			reached[f[0]] = true
		}
	}
	reached["movq"] = true // covered by the SSE probe loop above
	for _, m := range x86_64.KnownMnemonics() {
		if condFamily.MatchString(m) || reached[m] {
			continue
		}
		t.Errorf("internal/native/x86_64 assembles %q and the self-host assembler does not reach it — port it, or the two assemblers have silently drifted", m)
	}
}
