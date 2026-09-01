package x86_64_test

// Generator-driven differential against GNU as (issue #7896): a form
// inventory covers every instruction shape the assembler supports, a seeded
// PRNG instantiates cases per form (register numbers weighted toward the
// encoding-quirk registers, immediates and displacements weighted to straddle
// each width boundary), and the resulting program must assemble byte-for-byte
// identically to GNU as. Forms where our encoding choice legitimately differs
// from gas while decoding to the same instruction compare objdump output
// instead (compareDecode).
//
// Tiers: the default run is a small smoke tier (a few cases per form);
// FERN_ASM_FUZZ=1 runs the deep tier (thousands of cases per form).
// FERN_ASM_FUZZ_SEED overrides the fixed default seed.

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
	"github.com/jakechampion/lang/internal/native/x86_64"
)

// compareMode says how a form's encodings are checked against the oracle.
type compareMode int

const (
	// compareBytes requires byte equality with the oracle.
	compareBytes compareMode = iota
	// compareDecode requires only that both encodings disassemble to the
	// same instructions (objdump on the raw bytes). Used for the forms where
	// gas/llvm pick a legal shorter encoding than we do: the accumulator
	// immediate forms (A8/A9, 04/05/0C/0D/…) and xchg-with-accumulator
	// (90+r), per the exclusion note on TestAssembleAgainstGNUAs.
	compareDecode
)

// asmForm is one row of the form inventory: a named template that generates
// instruction units. A unit is a single instruction line, or — for
// multi=true forms — a self-contained multi-line snippet with labels.
type asmForm struct {
	name string
	mode compareMode
	// multi marks label-bearing snippet forms: they cannot be minimized to a
	// single line, and the llvm-mc lane skips them (show-encoding emits
	// unresolved fixups for label operands).
	multi bool
	gen   func(r *rand.Rand, i int) string
}

func fuzzCaseCount() int {
	if os.Getenv("FERN_ASM_FUZZ") != "" {
		return 2000
	}
	return 8
}

func fuzzSeed(t *testing.T) int64 {
	s := os.Getenv("FERN_ASM_FUZZ_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 0, 64)
	if err != nil {
		t.Fatalf("bad FERN_ASM_FUZZ_SEED %q: %v", s, err)
	}
	return n
}

// formRand returns the deterministic PRNG for one form: seeded from the
// global seed plus the form name, so adding or reordering forms does not
// shift the cases of the others, and the llvm-mc lane regenerates the exact
// same units.
func formRand(seed int64, name string) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte(name))
	return rand.New(rand.NewSource(seed ^ int64(h.Sum64())))
}

func formUnits(f asmForm, r *rand.Rand, n int) []string {
	units := make([]string, n)
	for i := range units {
		units[i] = f.gen(r, i)
	}
	return units
}

// ---------------------------------------------------------------------------
// Value spaces.

var gpNames = map[int][]string{
	64: {"rax", "rcx", "rdx", "rbx", "rsp", "rbp", "rsi", "rdi", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15"},
	32: {"eax", "ecx", "edx", "ebx", "esp", "ebp", "esi", "edi", "r8d", "r9d", "r10d", "r11d", "r12d", "r13d", "r14d", "r15d"},
	16: {"ax", "cx", "dx", "bx", "sp", "bp", "si", "di", "r8w", "r9w", "r10w", "r11w", "r12w", "r13w", "r14w", "r15w"},
	8:  {"al", "cl", "dl", "bl", "spl", "bpl", "sil", "dil", "r8b", "r9b", "r10b", "r11b", "r12b", "r13b", "r14b", "r15b"},
}

// quirkIdx weights the register pick toward the numbers with encoding
// special cases: 4 (rsp: SIB escape), 5 (rbp: forced disp), 12 (r12) and
// 13 (r13), which repeat those quirks through REX.B.
var quirkIdx = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 4, 5, 12, 13, 4, 5, 12, 13}

func pickReg(r *rand.Rand, width int) string { return gpNames[width][quirkIdx[r.Intn(len(quirkIdx))]] }

// pickRegNonAcc avoids the accumulator, whose immediate forms gas shortens
// to the dedicated A8/04/05/… opcodes (see compareDecode).
func pickRegNonAcc(r *rand.Rand, width int) string {
	for {
		if n := pickReg(r, width); n != gpNames[width][0] {
			return n
		}
	}
}

func pickXmm(r *rand.Rand) string { return fmt.Sprintf("xmm%d", r.Intn(16)) }

func pick(r *rand.Rand, xs []string) string { return xs[r.Intn(len(xs))] }

var widths = []int{8, 16, 32, 64}
var wideWidths = []int{16, 32, 64}

var sizeName = map[int]string{8: "byte", 16: "word", 32: "dword", 64: "qword"}

// aluImm draws an operation immediate for the given width, weighted to
// straddle the imm8/imm32 selection boundary.
func aluImm(r *rand.Rand, width int) int64 {
	boundary := []int64{-128, -127, -1, 0, 1, 126, 127}
	switch width {
	case 8:
		boundary = append(boundary, 128, 200, 255)
	case 16:
		boundary = append(boundary, -32768, -129, 128, 129, 255, 256, 300, 32767)
	default:
		boundary = append(boundary, -2147483648, -32769, -129, 128, 129, 255, 256,
			32767, 32768, 65535, 65536, 1000000, 2147483647)
	}
	if r.Intn(2) == 0 {
		return boundary[r.Intn(len(boundary))]
	}
	switch width {
	case 8:
		return int64(r.Intn(384)) - 128
	case 16:
		return int64(r.Intn(1<<16)) - 1<<15
	default:
		return int64(r.Int31()) - int64(r.Int31())/2
	}
}

var dispPool = []int64{0, 1, -1, 2, 4, 8, -4, -8, 16, -16, 32, 64, 100, 124, 127,
	-124, -127, -128, 128, -129, 132, 255, 256, -256, 1000, -1000, 4095, 4096,
	32767, -32768, 0x12345, -0x12345, 1 << 28, -(1 << 28)}

func pickDisp(r *rand.Rand) int64 {
	if r.Intn(3) == 0 {
		return int64(int32(r.Uint32()) / 16)
	}
	return dispPool[r.Intn(len(dispPool))]
}

func dispStr(d int64) string {
	if d < 0 {
		return fmt.Sprintf("-%d", -d)
	}
	return fmt.Sprintf("+%d", d)
}

var scales = []int{1, 2, 4, 8}

// memOp generates a bare memory operand across the SIB shapes: base only,
// base+disp, base+index*scale(+disp), base+index, and index-only.
func memOp(r *rand.Rand) string {
	base := pickReg(r, 64)
	// Index must not be rsp (no encoding); r12 as index is fine and quirky.
	idx := base
	for idx == "rsp" || idx == base {
		idx = pickReg(r, 64)
	}
	scale := scales[r.Intn(len(scales))]
	switch r.Intn(6) {
	case 0:
		return "[" + base + "]"
	case 1:
		return fmt.Sprintf("[%s%s]", base, dispStr(pickDisp(r)))
	case 2:
		return fmt.Sprintf("[%s+%s*%d]", base, idx, scale)
	case 3:
		return fmt.Sprintf("[%s+%s*%d%s]", base, idx, scale, dispStr(pickDisp(r)))
	case 4:
		return fmt.Sprintf("[%s+%s]", base, idx)
	default:
		return fmt.Sprintf("[%s*%d%s]", idx, scale, dispStr(pickDisp(r)))
	}
}

func sizedMem(r *rand.Rand, width int) string {
	return sizeName[width] + " ptr " + memOp(r)
}

// maybeSizedMem sometimes adds a redundant size prefix when a register
// operand already fixes the width, exercising both parser paths.
func maybeSizedMem(r *rand.Rand, width int) string {
	if r.Intn(2) == 0 {
		return sizedMem(r, width)
	}
	return memOp(r)
}

var ccNames = []string{"o", "no", "b", "c", "nae", "ae", "nb", "nc", "e", "z",
	"ne", "nz", "be", "na", "a", "nbe", "s", "ns", "p", "np",
	"l", "nge", "ge", "nl", "le", "ng", "g", "nle"}

// ---------------------------------------------------------------------------
// Form inventory.

var aluMnems = []string{"add", "or", "adc", "sbb", "and", "sub", "xor", "cmp"}
var shiftMnems = []string{"rol", "ror", "rcl", "rcr", "shl", "shr", "sar"}
var unaryMnems = []string{"neg", "not", "mul", "imul", "div", "idiv"}
var btMnems = []string{"bt", "bts", "btr", "btc"}
var bitcntMnems = []string{"bsf", "bsr", "lzcnt", "tzcnt", "popcnt"}
var lockAluMnems = []string{"add", "or", "adc", "sbb", "and", "sub", "xor"}
var stringUnits = []string{
	"cld", "std", "movsb", "movsw", "movsd", "movsq", "stosb", "stosw", "stosd",
	"stosq", "cmpsb", "cmpsw", "cmpsd", "cmpsq", "scasb", "scasw", "scasd",
	"scasq", "lodsb", "lodsw", "lodsd", "lodsq",
	"rep movsb", "rep movsw", "rep movsq", "rep stosb", "rep stosd", "rep stosq",
	"repe cmpsb", "repe cmpsq", "repz cmpsw", "repne scasb", "repnz scasd",
}
var nullaryUnits = []string{
	"ret", "leave", "ud2", "nop", "int3", "pause", "mfence", "lfence", "sfence",
	"cbw", "cwde", "cdqe", "cwd", "cdq", "cqo", "pushfq", "popfq", "syscall",
}

// sseTableMnems mirrors the mnemonics routed through the sseOps table
// (xmm <- xmm/mem two-operand shape). movaps/movapd are load-direction only
// there, which is fine for this shape.
var sseTableMnems = []string{
	"addsd", "subsd", "mulsd", "divsd", "sqrtsd", "addss", "subss", "mulss",
	"divss", "sqrtss", "minsd", "maxsd", "minss", "maxss", "ucomisd", "comisd",
	"ucomiss", "comiss", "cvtss2sd", "cvtsd2ss", "movapd", "movaps", "xorpd",
	"xorps", "andpd", "andps", "movdqu", "movdqa", "pcmpeqb", "pcmpeqw",
	"pcmpeqd", "pcmpgtb", "pcmpgtw", "pcmpgtd", "punpcklbw", "punpcklwd",
	"punpckldq", "punpcklqdq", "punpckhbw", "punpckhwd", "punpckhdq",
	"punpckhqdq", "por", "pand", "pxor", "pandn", "paddb", "paddw", "paddd",
	"paddq", "psubb", "psubw", "psubd", "psubq", "paddusb", "psubusb", "paddsb",
	"psubsb", "pavgb", "pminub", "pmaxub", "pminsw", "pmaxsw", "pmullw",
	"pmulhw", "pmulhuw", "pmuludq", "psadbw", "packsswb", "packuswb", "packssdw",
	"psllw", "pslld", "psllq", "psrlw", "psrld", "psrlq", "psraw", "psrad",
	"addpd", "subpd", "mulpd", "divpd", "sqrtpd", "minpd", "maxpd", "addps",
	"subps", "mulps", "divps", "sqrtps", "minps", "maxps", "andnpd", "orpd",
	"andnps", "orps", "unpcklpd", "unpckhpd", "cvtdq2ps", "cvtps2dq",
	"cvttps2dq", "cvtdq2pd", "cvtpd2dq", "cvttpd2dq",
}
var sse38Mnems = []string{"ptest", "pmulld", "pminsb", "pminsd", "pminuw",
	"pminud", "pmaxsb", "pmaxsd", "pmaxuw", "pmaxud"}
var vecShiftMnems = []string{"psllw", "psraw", "psrlw", "pslld", "psrad",
	"psrld", "psllq", "psrlq", "pslldq", "psrldq"}

// padPool is the branch-distance pool, in single-byte nops, straddling the
// rel8 limit in both directions (backward crosses at ~126 because the branch
// bytes themselves count).
var padPool = []int{0, 1, 2, 4, 8, 30, 100, 120, 121, 122, 123, 124, 125, 126,
	127, 128, 129, 130, 131, 132, 140, 200}

func x86Forms() []asmForm {
	nl := func(s string) string { return s + "\n" }
	forms := []asmForm{
		{name: "alu_rr", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			return nl(fmt.Sprintf("%s %s, %s", pick(r, aluMnems), pickReg(r, w), pickReg(r, w)))
		}},
		{name: "alu_rm", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			return nl(fmt.Sprintf("%s %s, %s", pick(r, aluMnems), pickReg(r, w), maybeSizedMem(r, w)))
		}},
		{name: "alu_mr", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			return nl(fmt.Sprintf("%s %s, %s", pick(r, aluMnems), maybeSizedMem(r, w), pickReg(r, w)))
		}},
		{name: "alu_ri", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			return nl(fmt.Sprintf("%s %s, %d", pick(r, aluMnems), pickRegNonAcc(r, w), aluImm(r, w)))
		}},
		{name: "alu_ri_acc", mode: compareDecode, gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			return nl(fmt.Sprintf("%s %s, %d", pick(r, aluMnems), gpNames[w][0], aluImm(r, w)))
		}},
		{name: "alu_mi", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			return nl(fmt.Sprintf("%s %s, %d", pick(r, aluMnems), sizedMem(r, w), aluImm(r, w)))
		}},
		{name: "test_rr_rm", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			reg := pickReg(r, w)
			switch r.Intn(3) {
			case 0:
				return nl(fmt.Sprintf("test %s, %s", reg, pickReg(r, w)))
			case 1:
				return nl(fmt.Sprintf("test %s, %s", reg, maybeSizedMem(r, w)))
			default:
				return nl(fmt.Sprintf("test %s, %s", maybeSizedMem(r, w), reg))
			}
		}},
		{name: "test_ri", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			return nl(fmt.Sprintf("test %s, %d", pickRegNonAcc(r, w), aluImm(r, w)))
		}},
		{name: "test_ri_acc", mode: compareDecode, gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			return nl(fmt.Sprintf("test %s, %d", gpNames[w][0], aluImm(r, w)))
		}},
		{name: "test_mi", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			return nl(fmt.Sprintf("test %s, %d", sizedMem(r, w), aluImm(r, w)))
		}},
		{name: "shift_ri", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			return nl(fmt.Sprintf("%s %s, %d", pick(r, shiftMnems), pickReg(r, w), 1+r.Intn(w-1)))
		}},
		{name: "shift_rcl", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			return nl(fmt.Sprintf("%s %s, cl", pick(r, shiftMnems), pickReg(r, w)))
		}},
		{name: "shift_mem", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("%s %s, %d", pick(r, shiftMnems), sizedMem(r, w), 1+r.Intn(w-1)))
			}
			return nl(fmt.Sprintf("%s %s, cl", pick(r, shiftMnems), sizedMem(r, w)))
		}},
		{name: "shld_shrd", gen: func(r *rand.Rand, _ int) string {
			w := wideWidths[r.Intn(3)]
			mnem := pick(r, []string{"shld", "shrd"})
			dst := pickReg(r, w)
			if r.Intn(3) == 0 {
				dst = sizedMem(r, w)
			}
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("%s %s, %s, cl", mnem, dst, pickReg(r, w)))
			}
			return nl(fmt.Sprintf("%s %s, %s, %d", mnem, dst, pickReg(r, w), 1+r.Intn(w-1)))
		}},
		{name: "movzx_movsx", gen: func(r *rand.Rand, _ int) string {
			dw := wideWidths[r.Intn(3)]
			sw := 8
			if dw > 16 && r.Intn(2) == 0 {
				sw = 16
			}
			mnem := pick(r, []string{"movzx", "movsx"})
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("%s %s, %s", mnem, pickReg(r, dw), pickReg(r, sw)))
			}
			return nl(fmt.Sprintf("%s %s, %s", mnem, pickReg(r, dw), sizedMem(r, sw)))
		}},
		{name: "movsxd", gen: func(r *rand.Rand, _ int) string {
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("movsxd %s, %s", pickReg(r, 64), pickReg(r, 32)))
			}
			return nl(fmt.Sprintf("movsxd %s, %s", pickReg(r, 64), sizedMem(r, 32)))
		}},
		{name: "lea", gen: func(r *rand.Rand, _ int) string {
			w := wideWidths[r.Intn(3)]
			return nl(fmt.Sprintf("lea %s, %s", pickReg(r, w), memOp(r)))
		}},
		{name: "bt_family", gen: func(r *rand.Rand, _ int) string {
			w := wideWidths[r.Intn(3)]
			mnem := pick(r, btMnems)
			switch r.Intn(4) {
			case 0:
				return nl(fmt.Sprintf("%s %s, %d", mnem, pickReg(r, w), r.Intn(w)))
			case 1:
				return nl(fmt.Sprintf("%s %s, %s", mnem, pickReg(r, w), pickReg(r, w)))
			case 2:
				return nl(fmt.Sprintf("%s %s, %s", mnem, memOp(r), pickReg(r, w)))
			default:
				return nl(fmt.Sprintf("%s %s, %d", mnem, sizedMem(r, w), r.Intn(w)))
			}
		}},
		{name: "cmov", gen: func(r *rand.Rand, _ int) string {
			w := wideWidths[r.Intn(3)]
			cc := pick(r, ccNames)
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("cmov%s %s, %s", cc, pickReg(r, w), pickReg(r, w)))
			}
			return nl(fmt.Sprintf("cmov%s %s, %s", cc, pickReg(r, w), maybeSizedMem(r, w)))
		}},
		{name: "setcc", gen: func(r *rand.Rand, _ int) string {
			return nl(fmt.Sprintf("set%s %s", pick(r, ccNames), pickReg(r, 8)))
		}},
		{name: "push_pop", gen: func(r *rand.Rand, _ int) string {
			switch r.Intn(6) {
			case 0:
				return nl("push " + pickReg(r, 64))
			case 1:
				return nl("pop " + pickReg(r, 64))
			case 2:
				return nl("push " + pickReg(r, 16))
			case 3:
				return nl(fmt.Sprintf("push %d", aluImm(r, 32)))
			case 4:
				return nl("push qword ptr " + memOp(r))
			default:
				return nl("pop qword ptr " + memOp(r))
			}
		}},
		{name: "inc_dec", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			mnem := pick(r, []string{"inc", "dec"})
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("%s %s", mnem, pickReg(r, w)))
			}
			return nl(fmt.Sprintf("%s %s", mnem, sizedMem(r, w)))
		}},
		{name: "unary_f7", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			mnem := pick(r, unaryMnems)
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("%s %s", mnem, pickReg(r, w)))
			}
			return nl(fmt.Sprintf("%s %s", mnem, sizedMem(r, w)))
		}},
		{name: "high_byte", gen: func(r *rand.Rand, _ int) string {
			// ah/ch/dh/bh cannot meet a REX-requiring operand, so they get
			// their own single-operand and same-class forms.
			hb := pick(r, []string{"ah", "ch", "dh", "bh"})
			low := pick(r, []string{"al", "cl", "dl", "bl"})
			switch r.Intn(4) {
			case 0:
				return nl(fmt.Sprintf("%s %s, %d", pick(r, shiftMnems), hb, 1+r.Intn(7)))
			case 1:
				return nl(fmt.Sprintf("%s %s", pick(r, []string{"inc", "dec", "neg", "not"}), hb))
			case 2:
				return nl(fmt.Sprintf("%s %s, %s", pick(r, aluMnems), hb, low))
			default:
				return nl(fmt.Sprintf("mov %s, %s", low, hb))
			}
		}},
		{name: "imul2", gen: func(r *rand.Rand, _ int) string {
			w := wideWidths[r.Intn(3)]
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("imul %s, %s", pickReg(r, w), pickReg(r, w)))
			}
			return nl(fmt.Sprintf("imul %s, %s", pickReg(r, w), maybeSizedMem(r, w)))
		}},
		{name: "imul3", gen: func(r *rand.Rand, _ int) string {
			w := wideWidths[r.Intn(3)]
			imm := aluImm(r, w)
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("imul %s, %s, %d", pickReg(r, w), pickReg(r, w), imm))
			}
			return nl(fmt.Sprintf("imul %s, %s, %d", pickReg(r, w), maybeSizedMem(r, w), imm))
		}},
		{name: "xchg", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			a, b := pickReg(r, w), pickReg(r, w)
			if w != 8 {
				// gas shortens xchg-with-accumulator to 90+r; that pairing
				// lives in xchg_acc below.
				a, b = pickRegNonAcc(r, w), pickRegNonAcc(r, w)
			}
			switch r.Intn(3) {
			case 0:
				return nl(fmt.Sprintf("xchg %s, %s", a, b))
			case 1:
				return nl(fmt.Sprintf("xchg %s, %s", maybeSizedMem(r, w), b))
			default:
				return nl(fmt.Sprintf("xchg %s, %s", a, maybeSizedMem(r, w)))
			}
		}},
		{name: "xchg_acc", mode: compareDecode, gen: func(r *rand.Rand, _ int) string {
			w := wideWidths[r.Intn(3)]
			other := pickRegNonAcc(r, w)
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("xchg %s, %s", gpNames[w][0], other))
			}
			return nl(fmt.Sprintf("xchg %s, %s", other, gpNames[w][0]))
		}},
		{name: "xadd_cmpxchg", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			mnem := pick(r, []string{"xadd", "cmpxchg"})
			lock := ""
			if r.Intn(2) == 0 {
				lock = "lock "
				return nl(fmt.Sprintf("%s%s %s, %s", lock, mnem, maybeSizedMem(r, w), pickReg(r, w)))
			}
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("%s %s, %s", mnem, pickReg(r, w), pickReg(r, w)))
			}
			return nl(fmt.Sprintf("%s %s, %s", mnem, maybeSizedMem(r, w), pickReg(r, w)))
		}},
		{name: "lock_rmw", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			switch r.Intn(4) {
			case 0:
				return nl(fmt.Sprintf("lock %s %s, %s", pick(r, lockAluMnems), maybeSizedMem(r, w), pickReg(r, w)))
			case 1:
				return nl(fmt.Sprintf("lock %s %s, %d", pick(r, lockAluMnems), sizedMem(r, w), aluImm(r, w)))
			case 2:
				return nl(fmt.Sprintf("lock %s %s", pick(r, []string{"inc", "dec", "neg", "not"}), sizedMem(r, w)))
			default:
				ww := wideWidths[r.Intn(3)]
				return nl(fmt.Sprintf("lock %s %s, %s", pick(r, []string{"bts", "btr", "btc"}), memOp(r), pickReg(r, ww)))
			}
		}},
		{name: "bswap", gen: func(r *rand.Rand, _ int) string {
			w := []int{32, 64}[r.Intn(2)]
			return nl("bswap " + pickReg(r, w))
		}},
		{name: "bitcount", gen: func(r *rand.Rand, _ int) string {
			w := wideWidths[r.Intn(3)]
			mnem := pick(r, bitcntMnems)
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("%s %s, %s", mnem, pickReg(r, w), pickReg(r, w)))
			}
			return nl(fmt.Sprintf("%s %s, %s", mnem, pickReg(r, w), maybeSizedMem(r, w)))
		}},
		{name: "mov_rr_rm_mr", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			switch r.Intn(3) {
			case 0:
				return nl(fmt.Sprintf("mov %s, %s", pickReg(r, w), pickReg(r, w)))
			case 1:
				return nl(fmt.Sprintf("mov %s, %s", pickReg(r, w), maybeSizedMem(r, w)))
			default:
				return nl(fmt.Sprintf("mov %s, %s", maybeSizedMem(r, w), pickReg(r, w)))
			}
		}},
		{name: "mov_ri", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			imm := aluImm(r, w)
			if w == 64 {
				big := []int64{0x80000000, 0xffffffff, 0x100000000, 0x123456789,
					-0x80000001, 1 << 62, -(1 << 62), -1}
				if r.Intn(2) == 0 {
					imm = big[r.Intn(len(big))]
				}
			}
			return nl(fmt.Sprintf("mov %s, %d", pickReg(r, w), imm))
		}},
		{name: "movabs", gen: func(r *rand.Rand, _ int) string {
			return nl(fmt.Sprintf("movabs %s, %d", pickReg(r, 64), int64(r.Uint64())))
		}},
		{name: "mov_mi", gen: func(r *rand.Rand, _ int) string {
			w := widths[r.Intn(4)]
			return nl(fmt.Sprintf("mov %s, %d", sizedMem(r, w), aluImm(r, w)))
		}},
		{name: "string_ops", gen: func(r *rand.Rand, _ int) string {
			return nl(pick(r, stringUnits))
		}},
		{name: "nullary", gen: func(r *rand.Rand, _ int) string {
			return nl(pick(r, nullaryUnits))
		}},
		{name: "indirect_branch", gen: func(r *rand.Rand, _ int) string {
			mnem := pick(r, []string{"call", "jmp"})
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("%s %s", mnem, pickReg(r, 64)))
			}
			return nl(fmt.Sprintf("%s qword ptr %s", mnem, memOp(r)))
		}},
		{name: "branch_labels", multi: true, gen: func(r *rand.Rand, i int) string {
			back := fmt.Sprintf("bf%d_a", i)
			fwd := fmt.Sprintf("bf%d_b", i)
			pad := func() string {
				return strings.Repeat("nop\n", padPool[r.Intn(len(padPool))])
			}
			branch := func(target string) string {
				switch r.Intn(4) {
				case 0:
					return "jmp " + target + "\n"
				case 1:
					return "call " + target + "\n"
				default:
					return "j" + pick(r, ccNames) + " " + target + "\n"
				}
			}
			var b strings.Builder
			b.WriteString(back + ":\n")
			b.WriteString(pad())
			b.WriteString(branch(back))
			b.WriteString(branch(fwd))
			b.WriteString(pad())
			if r.Intn(4) == 0 {
				// An alignment pad between a shrinking branch and its
				// target is re-sized against the relaxed layout.
				b.WriteString(".p2align 4\n")
			}
			b.WriteString(fwd + ":\nret\n")
			return b.String()
		}},
		{name: "sse_table", gen: func(r *rand.Rand, _ int) string {
			mnem := pick(r, sseTableMnems)
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("%s %s, %s", mnem, pickXmm(r), pickXmm(r)))
			}
			return nl(fmt.Sprintf("%s %s, %s", mnem, pickXmm(r), memOp(r)))
		}},
		{name: "sse38", gen: func(r *rand.Rand, _ int) string {
			mnem := pick(r, sse38Mnems)
			if r.Intn(2) == 0 {
				return nl(fmt.Sprintf("%s %s, %s", mnem, pickXmm(r), pickXmm(r)))
			}
			return nl(fmt.Sprintf("%s %s, %s", mnem, pickXmm(r), memOp(r)))
		}},
		{name: "sse_shift_imm", gen: func(r *rand.Rand, _ int) string {
			return nl(fmt.Sprintf("%s %s, %d", pick(r, vecShiftMnems), pickXmm(r), 1+r.Intn(15)))
		}},
		{name: "movdq_store", gen: func(r *rand.Rand, _ int) string {
			return nl(fmt.Sprintf("%s %s, %s", pick(r, []string{"movdqu", "movdqa"}), memOp(r), pickXmm(r)))
		}},
		{name: "movq_movd_gpr", gen: func(r *rand.Rand, _ int) string {
			w, mnem := 64, "movq"
			if r.Intn(2) == 0 {
				w, mnem = 32, "movd"
			}
			switch r.Intn(3) {
			case 0:
				return nl(fmt.Sprintf("%s %s, %s", mnem, pickXmm(r), pickReg(r, w)))
			case 1:
				return nl(fmt.Sprintf("%s %s, %s", mnem, pickReg(r, w), pickXmm(r)))
			default:
				return nl(fmt.Sprintf("movq %s, %s", pickXmm(r), pickXmm(r)))
			}
		}},
		{name: "movss_movsd", gen: func(r *rand.Rand, _ int) string {
			mnem := pick(r, []string{"movsd", "movss", "movups", "movupd"})
			sz := "qword"
			if mnem == "movss" {
				sz = "dword"
			}
			mem := memOp(r)
			if mnem == "movsd" || mnem == "movss" {
				mem = sz + " ptr " + mem
			}
			switch r.Intn(3) {
			case 0:
				return nl(fmt.Sprintf("%s %s, %s", mnem, pickXmm(r), pickXmm(r)))
			case 1:
				return nl(fmt.Sprintf("%s %s, %s", mnem, pickXmm(r), mem))
			default:
				return nl(fmt.Sprintf("%s %s, %s", mnem, mem, pickXmm(r)))
			}
		}},
		{name: "sse_cvt", gen: func(r *rand.Rand, _ int) string {
			w := []int{32, 64}[r.Intn(2)]
			switch r.Intn(4) {
			case 0:
				return nl(fmt.Sprintf("%s %s, %s", pick(r, []string{"cvtsi2sd", "cvtsi2ss"}), pickXmm(r), pickReg(r, w)))
			case 1:
				// The memory size is the INTEGER source width and picks REX.W.
				return nl(fmt.Sprintf("%s %s, %s", pick(r, []string{"cvtsi2sd", "cvtsi2ss"}), pickXmm(r), sizedMem(r, w)))
			case 2:
				return nl(fmt.Sprintf("%s %s, %s", pick(r, []string{"cvttsd2si", "cvttss2si", "cvtsd2si", "cvtss2si"}), pickReg(r, w), pickXmm(r)))
			default:
				// A memory source is the scalar float, so its size follows
				// the mnemonic (sd = qword, ss = dword), not the GPR width.
				if r.Intn(2) == 0 {
					return nl(fmt.Sprintf("cvttsd2si %s, qword ptr %s", pickReg(r, w), memOp(r)))
				}
				return nl(fmt.Sprintf("cvttss2si %s, dword ptr %s", pickReg(r, w), memOp(r)))
			}
		}},
		{name: "sse_imm8", gen: func(r *rand.Rand, _ int) string {
			switch r.Intn(3) {
			case 0:
				return nl(fmt.Sprintf("%s %s, %s, %d", pick(r, []string{"roundsd", "roundss"}), pickXmm(r), pickXmm(r), r.Intn(12)))
			case 1:
				return nl(fmt.Sprintf("%s %s, %s, %d", pick(r, []string{"pshufd", "shufps", "shufpd"}), pickXmm(r), pickXmm(r), r.Intn(256)))
			default:
				return nl(fmt.Sprintf("%s %s, %s, %d", pick(r, []string{"pcmpistri", "pcmpestri"}), pickXmm(r), pickXmm(r), r.Intn(64)))
			}
		}},
		{name: "sse_mask", gen: func(r *rand.Rand, _ int) string {
			// r32 destinations only: gas sets REX.W on the r64 spelling of
			// these mask reads where our encoder never would.
			return nl(fmt.Sprintf("%s %s, %s", pick(r, []string{"pmovmskb", "movmskps", "movmskpd"}), pickReg(r, 32), pickXmm(r)))
		}},
		{name: "pextr_pinsr", gen: func(r *rand.Rand, _ int) string {
			switch r.Intn(4) {
			case 0:
				mnem := pick(r, []string{"pextrb", "pextrw", "pextrd"})
				return nl(fmt.Sprintf("%s %s, %s, %d", mnem, pickReg(r, 32), pickXmm(r), r.Intn(8)))
			case 1:
				return nl(fmt.Sprintf("pextrq %s, %s, %d", pickReg(r, 64), pickXmm(r), r.Intn(2)))
			case 2:
				mnem := pick(r, []string{"pinsrb", "pinsrw", "pinsrd"})
				return nl(fmt.Sprintf("%s %s, %s, %d", mnem, pickXmm(r), pickReg(r, 32), r.Intn(8)))
			default:
				return nl(fmt.Sprintf("pinsrq %s, %s, %d", pickXmm(r), pickReg(r, 64), r.Intn(2)))
			}
		}},
		{name: "crc32", gen: func(r *rand.Rand, _ int) string {
			if r.Intn(2) == 0 {
				sw := []int{8, 16, 32}[r.Intn(3)]
				return nl(fmt.Sprintf("crc32 %s, %s", pickReg(r, 32), pickReg(r, sw)))
			}
			sw := []int{8, 64}[r.Intn(2)]
			return nl(fmt.Sprintf("crc32 %s, %s", pickReg(r, 64), pickReg(r, sw)))
		}},
	}
	sort.Slice(forms, func(i, j int) bool { return forms[i].name < forms[j].name })
	return forms
}

// ---------------------------------------------------------------------------
// Oracle plumbing.

func ourBytes(t *testing.T, src string) ([]byte, error) {
	t.Helper()
	text, _, err := x86_64.AssembleProgram(src, elf.TextVAddr)
	return text, err
}

// objdumpX86 disassembles raw bytes and returns the normalized instruction
// texts, one per instruction.
func objdumpX86(t *testing.T, objdump string, code []byte) []string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "code.bin")
	if err := os.WriteFile(bin, code, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(objdump, "-D", "-b", "binary", "-m", "i386:x86-64", "-M", "intel", bin).Output()
	if err != nil {
		t.Fatalf("objdump: %v", err)
	}
	return parseObjdumpInsns(string(out))
}

var objdumpLine = regexp.MustCompile(`^\s*[0-9a-f]+:\t(?:[0-9a-f]{2} )+\s*\t(.*)$`)

func parseObjdumpInsns(out string) []string {
	var insns []string
	for _, line := range strings.Split(out, "\n") {
		m := objdumpLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		insns = append(insns, normalizeInsn(m[1]))
	}
	return insns
}

// normalizeInsn canonicalizes one disassembled instruction: whitespace is
// collapsed, and xchg's operands are sorted (the instruction is symmetric,
// and the 90+r short form decodes with the operands the other way round
// from 87 /r).
func normalizeInsn(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if rest, ok := strings.CutPrefix(s, "xchg "); ok {
		ops := strings.Split(rest, ",")
		sort.Strings(ops)
		s = "xchg " + strings.Join(ops, ",")
	}
	return s
}

func findObjdump(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("objdump")
	if err != nil {
		t.Skip("objdump not on PATH")
	}
	return p
}

// TestFuzzEncodingsAgainstGNUAs is the generator-driven differential: for
// each form, N seeded cases are assembled by AssembleProgram and by GNU as
// and compared per the form's mode. On a mismatch the failing batch is
// minimized to a single unit and printed as a ready-to-pin row.
func TestFuzzEncodingsAgainstGNUAs(t *testing.T) {
	as, objcopy := findX86Binutils(t)
	seed := fuzzSeed(t)
	n := fuzzCaseCount()
	for _, f := range x86Forms() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			units := formUnits(f, formRand(seed, f.name), n)
			src := strings.Join(units, "")
			got, err := ourBytes(t, src)
			if err != nil {
				minimizeAssembleError(t, f, units)
				t.Fatalf("AssembleProgram: %v", err)
			}
			want := gnuAsX86Text(t, as, objcopy, src)
			if f.mode == compareDecode {
				objdump := findObjdump(t)
				gotI := objdumpX86(t, objdump, got)
				wantI := objdumpX86(t, objdump, want)
				if len(gotI) != len(wantI) {
					t.Fatalf("decode counts differ: ours %d insns, gas %d", len(gotI), len(wantI))
				}
				for i := range gotI {
					if gotI[i] != wantI[i] {
						t.Errorf("decode differs at insn %d:\n ours %s\n gas  %s\n(seed %d, form %s)",
							i, gotI[i], wantI[i], seed, f.name)
						return
					}
				}
				return
			}
			if !bytes.Equal(got, want) {
				minimizeMismatch(t, f, units, as, objcopy, seed)
			}
		})
	}
}

// minimizeAssembleError re-runs each unit alone to name the one our
// assembler rejects.
func minimizeAssembleError(t *testing.T, f asmForm, units []string) {
	t.Helper()
	for _, u := range units {
		if _, err := ourBytes(t, u); err != nil {
			t.Errorf("unit fails to assemble:\n%s error: %v", u, err)
			return
		}
	}
}

// minimizeMismatch re-assembles each unit alone with both assemblers and
// prints the first divergence as a ready-to-pin test row.
func minimizeMismatch(t *testing.T, f asmForm, units []string, as, objcopy string, seed int64) {
	t.Helper()
	for _, u := range units {
		got, err := ourBytes(t, u)
		if err != nil {
			t.Fatalf("unit stopped assembling alone:\n%s error: %v", u, err)
		}
		want := gnuAsX86Text(t, as, objcopy, u)
		if !bytes.Equal(got, want) {
			t.Fatalf("encoding differs from GNU as (seed %d, form %s) — pin as:\n"+
				"source:\n%s ours: % x\n gas:  % x", seed, f.name, u, got, want)
		}
	}
	t.Fatalf("batch bytes differ from GNU as but every unit matches alone (form %s, seed %d): "+
		"layout/relaxation divergence across units", f.name, seed)
}
