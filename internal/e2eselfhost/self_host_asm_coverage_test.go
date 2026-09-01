package e2eselfhost

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
	"github.com/jakechampion/lang/internal/native/elf"
	"github.com/jakechampion/lang/internal/native/x86_64"
)

// Coverage parity between the self-host assemblers and the native (Go)
// assemblers that serve as their oracles, at Go speed: the mnemonic set each
// self-host dispatch accepts is read out of the Fern source as data (the
// same pattern as internal/caps/selfhost_parity_test.go) and every entry is
// probed against the native assembler with a representative instruction.
//
// The rule these tests enforce (#6075): teach the oracle first. A mnemonic
// the self-host emits or accepts that the native assembler cannot encode is
// a differential that can never run, and the two lists have already drifted
// in both directions — movn was listed with no branch to assemble it
// (#6060), and the unary FP family had encoders but was missing from the
// list (#6044). Nothing here builds the self-host compiler; both tests are
// pure text extraction plus native-assembler probes.

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

// arm64KnownHelpers are the predicate / base-table helpers arm64_gas_known
// consults instead of spelling those mnemonics as literals in its own body
// (the #7886 families and the scalar-FP tables). Their bodies are extracted
// alongside arm64_gas_known's so every mnemonic the assembler accepts is
// enumerated; a helper that disappears fails loudly in fernFnBody rather
// than silently shrinking the probed surface.
var arm64KnownHelpers = []string{
	"arm64_gas_is_carry", "arm64_gas_is_mulwide", "arm64_gas_is_widening",
	"arm64_gas_is_logical2", "arm64_gas_is_bitfield", "arm64_gas_is_condsel",
	"arm64_gas_is_excl_ld", "arm64_gas_is_excl_st", "arm64_gas_is_acqrel",
	"arm64_gas_is_unscaled2",
	"arm64_fp3_base", "arm64_fp4_base", "arm64_funary_d_base",
}

// TestSelfHostAsmCoverageArm64 pins the self-host arm64 assembler's
// mnemonic allow-list (arm64_gas_known in arm64_native.fern, plus the
// family helpers it consults) to the native arm64 assembler: every
// mnemonic the self-host claims to handle must assemble through
// internal/native/arm64. The b.<cond> / b<cond> aliases are
// pattern-matched in the .fern source, not listed, so they are pinned
// here by explicit probes instead of extraction.
func TestSelfHostAsmCoverageArm64(t *testing.T) {
	const path = "../../examples/self_host/arm64_native.fern"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	body := fernFnBody(t, string(b), path, "arm64_gas_known")
	for _, helper := range arm64KnownHelpers {
		body += "\n" + fernFnBody(t, string(b), path, helper)
	}
	mnemonics := mnemonicCases(body, "mnem")

	// One representative, currently-valid instruction per mnemonic in the
	// self-host's dialect. adrp is resolved at the program level (it needs a
	// data symbol), so its probe carries a .rodata section; branches carry
	// their label.
	probes := map[string]string{
		"mov":    "mov x0, #42",
		"movz":   "movz x8, #93",
		"movk":   "movk x3, #0xabcd",
		"movn":   "movn x0, #1",
		"add":    "add x0, x1, #1",
		"sub":    "sub x4, x5, x6",
		"cmp":    "cmp x1, #5",
		"neg":    "neg x0, x1",
		"mul":    "mul x0, x1, x2",
		"ldr":    "ldr x0, [x1, #16]",
		"str":    "str x2, [x3, #8]",
		"ldur":   "ldur x0, [x1, #-8]",
		"stur":   "stur x0, [x1, #-8]",
		"stp":    "stp x29, x30, [sp, #-16]!",
		"ldp":    "ldp x29, x30, [sp], #16",
		"b":      "b l0\nl0:\nret",
		"bl":     "bl l0\nl0:\nret",
		"blr":    "blr x1",
		"ret":    "ret",
		"svc":    "svc #0",
		"cbz":    "cbz x0, l0\nl0:\nret",
		"cbnz":   "cbnz w1, l0\nl0:\nret",
		"tbz":    "tbz x0, #0, l0\nl0:\nret",
		"tbnz":   "tbnz x1, #63, l0\nl0:\nret",
		"ubfx":   "ubfx x1, x1, #56, #4",
		"cset":   "cset x0, ne",
		"adrp":   "adrp x0, sym\n.section .rodata\nsym:\n.quad 0",
		"sxtw":   "sxtw x0, w1",
		"sbfiz":  "sbfiz x1, x2, #3, #32",
		"lsl":    "lsl x0, x1, #4",
		"lsr":    "lsr x0, x1, x2",
		"asr":    "asr x0, x1, #4",
		"cmn":    "cmn x1, #5",
		"csel":   "csel x0, x1, x2, eq",
		"and":    "and x0, x1, x2",
		"orr":    "orr x0, x1, x2",
		"eor":    "eor x0, x1, x2",
		"subs":   "subs x2, x2, #1",
		"udiv":   "udiv x0, x1, x2",
		"sdiv":   "sdiv x0, x1, x2",
		"msub":   "msub x0, x1, x2, x3",
		"rev16":  "rev16 w0, w19",
		"clz":    "clz x0, x1",
		"rbit":   "rbit x0, x0",
		"nop":    "nop",
		"br":     "br x5",
		"sbfx":   "sbfx x1, x2, #3, #8",
		"sxtb":   "sxtb x0, w1",
		"sxth":   "sxth w0, w1",
		"uxtb":   "uxtb w0, w1",
		"uxth":   "uxth w0, w1",
		"ldrsb":  "ldrsb x0, [x1, #4]",
		"ldrsh":  "ldrsh w3, [x2, #62]",
		"cnt":    "cnt v0.8b, v0.8b",
		"addv":   "addv b0, v0.8b",
		"ld1":    "ld1 {v1.16b}, [x0]",
		"cmeq":   "cmeq v1.16b, v1.16b, v0.16b",
		"cmlt":   "cmlt v0.16b, v0.16b, #0",
		"shrn":   "shrn v1.8b, v1.8h, #4",
		"dup":    "dup v0.16b, w1",
		"ldrb":   "ldrb w4, [x5, #1]",
		"strb":   "strb w6, [x7, #2]",
		"ldrh":   "ldrh w0, [x1, #4]",
		"strh":   "strh w2, [x3, #6]",
		"ldrsw":  "ldrsw x0, [x1, #4]",
		"mrs":    "mrs x9, cntvct_el0",
		"msr":    "msr tpidr_el0, x9",
		"adr":    "adr x0, l0\nl0:\nret",
		"adc":    "adc x0, x1, x2",
		"adcs":   "adcs x0, x1, x2",
		"adds":   "adds x1, x2, x3",
		"sbc":    "sbc x0, x1, x2",
		"sbcs":   "sbcs w0, w1, w2",
		"ngc":    "ngc x0, x1",
		"ngcs":   "ngcs w0, w1",
		"umulh":  "umulh x0, x1, x2",
		"smulh":  "smulh x0, x1, x2",
		"madd":   "madd x0, x1, x2, x3",
		"smull":  "smull x0, w1, w2",
		"umull":  "umull x0, w1, w2",
		"smaddl": "smaddl x0, w1, w2, x3",
		"umaddl": "umaddl x0, w1, w2, x3",
		"smsubl": "smsubl x0, w1, w2, x3",
		"umsubl": "umsubl x0, w1, w2, x3",
		"tst":    "tst x0, x1",
		"ands":   "ands x0, x1, x2",
		"bic":    "bic x0, x1, x2",
		"bics":   "bics x0, x1, x2",
		"orn":    "orn x0, x1, x2",
		"eon":    "eon x0, x1, x2",
		"mvn":    "mvn x0, x1",
		"negs":   "negs x0, x1",
		"extr":   "extr x0, x1, x2, #12",
		"ror":    "ror x0, x1, #7",
		"bfi":    "bfi x0, x1, #4, #8",
		"bfxil":  "bfxil x0, x1, #4, #8",
		"ubfiz":  "ubfiz x0, x1, #3, #16",
		"ccmp":   "ccmp x0, x1, #0, eq",
		"ccmn":   "ccmn x0, #9, #15, lt",
		"csinc":  "csinc x0, x1, x2, lt",
		"csinv":  "csinv x0, x1, x2, lt",
		"csneg":  "csneg x0, x1, x2, lt",
		"cinc":   "cinc x0, x1, lt",
		"cinv":   "cinv x0, x1, lt",
		"cneg":   "cneg x0, x1, lt",
		"csetm":  "csetm x0, lt",
		"rev":    "rev x0, x1",
		"rev32":  "rev32 x0, x1",
		"cls":    "cls x0, x1",
		"ldxr":   "ldxr x0, [x1]",
		"ldxrb":  "ldxrb w0, [x1]",
		"ldxrh":  "ldxrh w0, [x1]",
		"ldaxr":  "ldaxr x0, [x1]",
		"ldaxrb": "ldaxrb w0, [x1]",
		"ldaxrh": "ldaxrh w0, [x1]",
		"stxr":   "stxr w2, x0, [x1]",
		"stxrb":  "stxrb w2, w0, [x1]",
		"stxrh":  "stxrh w2, w0, [x1]",
		"stlxr":  "stlxr w2, x0, [x1]",
		"stlxrb": "stlxrb w2, w0, [x1]",
		"stlxrh": "stlxrh w2, w0, [x1]",
		"ldar":   "ldar x0, [x1]",
		"ldarb":  "ldarb w0, [x1]",
		"ldarh":  "ldarh w0, [x1]",
		"stlr":   "stlr x0, [x1]",
		"stlrb":  "stlrb w0, [x1]",
		"stlrh":  "stlrh w0, [x1]",
		"dmb":    "dmb ish",
		"dsb":    "dsb sy",
		"isb":    "isb",
		"ldurb":  "ldurb w0, [x1, #-1]",
		"sturb":  "sturb w0, [x1, #-1]",
		"ldurh":  "ldurh w0, [x1, #-2]",
		"sturh":  "sturh w0, [x1, #-2]",
		"ldursb": "ldursb x0, [x1, #-1]",
		"ldursh": "ldursh w0, [x1, #-2]",
		"ldursw": "ldursw x0, [x1, #-4]",
		"fadd":   "fadd d0, d1, d2",
		"fsub":   "fsub d0, d1, d2",
		"fmul":   "fmul d0, d1, d2",
		"fdiv":   "fdiv d0, d1, d2",
		"fnmul":  "fnmul d0, d1, d2",
		"fmin":   "fmin d0, d1, d2",
		"fmax":   "fmax d0, d1, d2",
		"fminnm": "fminnm s0, s1, s2",
		"fmaxnm": "fmaxnm s0, s1, s2",
		"fmadd":  "fmadd d0, d1, d2, d3",
		"fmsub":  "fmsub d0, d1, d2, d3",
		"fnmadd": "fnmadd s0, s1, s2, s3",
		"fnmsub": "fnmsub s0, s1, s2, s3",
		"fcsel":  "fcsel d0, d1, d2, lt",
		"fccmp":  "fccmp d0, d1, #15, lt",
		"fcmp":   "fcmp d1, d2",
		"fcmpe":  "fcmpe d1, #0.0",
		"fmov":   "fmov d0, d1",
		"fcvtzs": "fcvtzs x0, d1",
		"fcvtzu": "fcvtzu w0, d1",
		"scvtf":  "scvtf d0, x1",
		"ucvtf":  "ucvtf d0, x1",
		"fcvt":   "fcvt d0, s1",
		"fneg":   "fneg d0, d1",
		"fabs":   "fabs d0, d1",
		"fsqrt":  "fsqrt d0, d1",
		"frinta": "frinta d0, d1",
		"frintm": "frintm d0, d1",
		"frintp": "frintp d0, d1",
		"frintz": "frintz d0, d1",
		"frintn": "frintn d0, d1",
	}
	assemble := func(probe string) error {
		_, _, err := arm64.AssembleProgram(".text\n"+probe+"\n", elf.TextVAddr)
		return err
	}
	probeMnemonics(t, path, mnemonics, probes, assemble)

	// The conditional-branch aliases arm64_gas_known accepts by pattern.
	for _, probe := range []string{
		"b.eq l0\nl0:\nret", "b.ne l0\nl0:\nret", "b.lt l0\nl0:\nret",
		"beq l0\nl0:\nret", "bge l0\nl0:\nret", "bhi l0\nl0:\nret",
	} {
		if err := assemble(probe); err != nil {
			t.Errorf("conditional-branch alias probe %q: native assembler rejects it: %v", probe, err)
		}
	}
}

// x86KnownHelpers are the lookup helpers x86_gas_emit consults instead of
// spelling those mnemonics as literals in its own body — the suffix
// families' base-mnemonic tables, the fixed/string byte tables, and the
// SSE tables (#7893). Their bodies are extracted alongside x86_gas_emit's
// so every mnemonic the assembler accepts is enumerated; a helper that
// disappears fails loudly in fernFnBody rather than silently shrinking
// the probed surface.
var x86KnownHelpers = []string{
	"x86_gas_alu_ext", "x86_gas_unary_ext", "x86_gas_incdec_ext",
	"x86_gas_shift_ext", "x86_gas_bt_idx",
	"x86_gas_fixed_op", "x86_gas_string_op",
	"x86_gas_sse_fp_op", "x86_gas_sse_int_op", "x86_gas_sse38_op",
	"x86_gas_vshift_op", "x86_gas_imm3a_op", "x86_gas_shuf_op",
}

// TestSelfHostAsmCoverageX86_64 pins the mnemonic set the self-host x86-64
// assembler dispatches on (x86_gas_emit in x86_native.fern plus the lookup
// helpers it consults, AT&T dialect) to the native Intel-syntax assembler:
// each AT&T mnemonic, translated to its Intel equivalent, must assemble
// through internal/native/x86_64.
//
// The probe line IS the AT&T-to-Intel mapping table: suffix-family bases
// (add/test/shl/...) probe suffix-less because Intel syntax carries the
// width on the operands; the renamed mnemonics are cqto to cqo,
// cbtw/cwtl/cltq/cwtd/cltd to cbw/cwde/cdqe/cwd/cdq, movabsq to movabs,
// movslq to movsxd, movzbq/movzbl/movzwq to movzx, and the l-suffixed
// string ops to their Intel d spellings (movsl = movsd, ...). The rep
// prefix is dispatched on "rep" with the string op matched inside that
// branch, so its probe covers the whole accepted set. cmovcc is
// dispatched by pattern (a condition table plus an optional width
// suffix), so it is pinned by the explicit probe loop at the end rather
// than by extraction.
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
		"ret":        "ret",
		"syscall":    "syscall",
		"leave":      "leave",
		"cqto":       "cqo",
		"cld":        "cld",
		"std":        "std",
		"nop":        "nop",
		"int3":       "int3",
		"pause":      "pause",
		"mfence":     "mfence",
		"lfence":     "lfence",
		"sfence":     "sfence",
		"cbtw":       "cbw",
		"cwtl":       "cwde",
		"cltq":       "cdqe",
		"cwtd":       "cwd",
		"cltd":       "cdq",
		"rep":        "rep movsb\nrep movsq\nrep stosb\nrep stosq",
		"lock":       "lock add qword ptr [rdi], 1",
		"movsb":      "movsb",
		"movsw":      "movsw",
		"movsl":      "movsd",
		"movsq":      "movsq",
		"stosb":      "stosb",
		"stosw":      "stosw",
		"stosl":      "stosd",
		"stosq":      "stosq",
		"lodsb":      "lodsb",
		"lodsw":      "lodsw",
		"lodsl":      "lodsd",
		"lodsq":      "lodsq",
		"scasb":      "scasb",
		"scasw":      "scasw",
		"scasl":      "scasd",
		"scasq":      "scasq",
		"cmpsb":      "cmpsb",
		"cmpsw":      "cmpsw",
		"cmpsl":      "cmpsd",
		"cmpsq":      "cmpsq",
		"sete":       "sete al",
		"setz":       "setz al",
		"setne":      "setne cl",
		"setnz":      "setnz cl",
		"setl":       "setl al",
		"setle":      "setle al",
		"setg":       "setg al",
		"setge":      "setge al",
		"setb":       "setb al",
		"setbe":      "setbe al",
		"seta":       "seta al",
		"setae":      "setae al",
		"sets":       "sets al",
		"setp":       "setp al",
		"setnp":      "setnp al",
		"setns":      "setns al",
		"call":       "call rax",
		"jmp":        "jmp rdx",
		"je":         "je l0\nl0:\nret",
		"jz":         "jz l0\nl0:\nret",
		"jne":        "jne l0\nl0:\nret",
		"jnz":        "jnz l0\nl0:\nret",
		"jge":        "jge l0\nl0:\nret",
		"jl":         "jl l0\nl0:\nret",
		"jle":        "jle l0\nl0:\nret",
		"jg":         "jg l0\nl0:\nret",
		"jb":         "jb l0\nl0:\nret",
		"jc":         "jc l0\nl0:\nret",
		"jae":        "jae l0\nl0:\nret",
		"jnc":        "jnc l0\nl0:\nret",
		"jbe":        "jbe l0\nl0:\nret",
		"ja":         "ja l0\nl0:\nret",
		"js":         "js l0\nl0:\nret",
		"jns":        "jns l0\nl0:\nret",
		"jp":         "jp l0\nl0:\nret",
		"pushq":      "push rbp",
		"popq":       "pop rbp",
		"leaq":       "lea rax, [rbp-8]",
		"movq":       "mov rax, rcx",
		"movabsq":    "movabs rax, 4294967296",
		"movabs":     "movabs rax, 4294967296",
		"movl":       "mov eax, ecx",
		"movb":       "mov byte ptr [rax], cl",
		"movzbq":     "movzx rax, byte ptr [rdi]",
		"movzbl":     "movzx eax, byte ptr [rdi]",
		"movzwq":     "movzx rax, word ptr [rdi]",
		"movslq":     "movsxd rax, ecx",
		"movsxd":     "movsxd rax, ecx",
		"movd":       "movd xmm0, eax",
		"add":        "add rax, rcx",
		"or":         "or rax, rcx",
		"adc":        "adc rax, rcx",
		"sbb":        "sbb rax, rcx",
		"and":        "and rax, rcx",
		"sub":        "sub rax, rcx",
		"xor":        "xor rax, rcx",
		"cmp":        "cmp rax, rcx",
		"test":       "test rax, rcx",
		"not":        "not rax",
		"neg":        "neg rax",
		"mul":        "mul rcx",
		"imul":       "imul rcx",
		"div":        "div rcx",
		"idiv":       "idiv rcx",
		"inc":        "inc rax",
		"dec":        "dec rax",
		"rol":        "rol rax, 3",
		"ror":        "ror rax, 3",
		"rcl":        "rcl rax, 3",
		"rcr":        "rcr rax, 3",
		"shl":        "shl rax, 3",
		"shr":        "shr rax, cl",
		"sar":        "sar rax, 63",
		"shld":       "shld rsi, rdi, cl",
		"shrd":       "shrd rsi, rdi, 5",
		"bt":         "bt rax, rcx",
		"bts":        "bts rax, 3",
		"btr":        "btr rax, rcx",
		"btc":        "btc rax, 3",
		"bswap":      "bswap rax",
		"xadd":       "xadd rax, rcx",
		"cmpxchg":    "cmpxchg qword ptr [rdi], rcx",
		"xchg":       "xchg qword ptr [rdi], rcx",
		"crc32":      "crc32 eax, cl",
		"bsfl":       "bsf eax, ecx",
		"bsrl":       "bsr eax, ecx",
		"lzcntl":     "lzcnt eax, ecx",
		"lzcntq":     "lzcnt rax, rcx",
		"tzcntl":     "tzcnt eax, ecx",
		"tzcntq":     "tzcnt rax, rcx",
		"popcntl":    "popcnt eax, ecx",
		"popcntq":    "popcnt rax, rcx",
		"cvttsd2si":  "cvttsd2si eax, xmm0",
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

	// The cmovcc aliases x86_gas_emit accepts by pattern: every condition
	// spelling the shared table knows, at the register-inferred widths.
	for _, cond := range []string{
		"o", "no", "b", "c", "nae", "ae", "nb", "nc", "e", "z", "ne", "nz",
		"be", "na", "a", "nbe", "s", "ns", "p", "np",
		"l", "nge", "ge", "nl", "le", "ng", "g", "nle",
	} {
		for _, probe := range []string{
			"cmov" + cond + " rax, rcx",
			"cmov" + cond + " eax, ecx",
		} {
			if err := assemble(probe); err != nil {
				t.Errorf("cmov alias probe %q: native assembler rejects it: %v", probe, err)
			}
		}
	}
}
