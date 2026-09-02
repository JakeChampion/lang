package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
	"github.com/jakechampion/lang/internal/native/x86_64"
)

// These tests byte-check the #7893 port of the #7886 instruction surface
// to the self-host x86-64 assembler (examples/self_host/x86_native.fern,
// AT&T dialect) against GNU as, through the in-process bench driver — the
// same encodings-only pattern as the arm64 twin
// (self_host_arm64_extops_gas_test.go). Every `want` below is what
// /usr/bin/as emits for the same AT&T line (read back with objdump —
// never hand-derived, since a wrong field placement usually still
// assembles as some other valid instruction). Families are checked at low
// AND extended register numbers, and unencodable near-miss shapes must be
// REFUSED (recorded in `unknown`), never folded into a wrong width — the
// silent-widening class this port audits for.
//
// Two deliberate, documented divergences from as are NOT pinned here, both
// places where the native assembler (the spec) picks a uniform encoding
// over as's special case: the accumulator-immediate short forms
// (04/05/…/A8/A9 for al/ax/eax/rax) and the movq xmm<->m64 forms (as
// picks F3 0F 7E / 66 0F D6; the native movqd family uses 66 REX.W 0F
// 6E/7E). Rows use non-accumulator registers, matching the existing
// `testq $imm, %rcx` note in self_host_x86_gas_test.go.

// pinnedX86 is one AT&T instruction line with its GNU-as byte sequence.
type pinnedX86 struct {
	asm  string
	want []byte
}

// buildX86AsmBenchDriver builds the stdin->bytes bench driver once per test.
func buildX86AsmBenchDriver(t *testing.T, gcc string) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "x86_asm_bench_run.fern")
	return buildSelfHostBin(t, gcc, dir, "x86_asm_bench_run.fern", "x86_asm_bench")
}

// assembleSelfHostX86 feeds GAS text to the driver and returns the
// assembled .text bytes. A refusal (a.unknown) is a failure here: the
// byte stream would be short and every later comparison would misalign.
func assembleSelfHostX86(t *testing.T, bin string, runner []string, snippet string) []byte {
	t.Helper()
	out := runX86BenchDriver(t, bin, runner, snippet, "-bytes")
	if refused := asmRefusals(out); len(refused) > 0 {
		t.Fatalf("the self-host assembler REFUSED %d line(s) of the snippet: %v", len(refused), refused)
	}
	var bs []byte
	for _, ln := range strings.Split(out, "\n") {
		if !strings.HasPrefix(ln, "byte ") {
			continue
		}
		var idx, val int
		if _, err := fmt.Sscanf(ln, "byte %d %d", &idx, &val); err != nil {
			t.Fatalf("unparsable byte line %q: %v", ln, err)
		}
		if idx != len(bs) {
			t.Fatalf("byte lines out of order: got index %d at position %d", idx, len(bs))
		}
		bs = append(bs, byte(val))
	}
	if len(bs) == 0 {
		t.Fatalf("driver printed no byte lines; output was:\n%s", out)
	}
	return bs
}

func runX86BenchDriver(t *testing.T, bin string, runner []string, snippet string, extra ...string) string {
	t.Helper()
	out := runCapture(t, "", runner, bin, []byte(snippet), extra...)
	return string(out)
}

// refusalsForX86 assembles a snippet the assembler is EXPECTED to reject
// and returns what it refused.
func refusalsForX86(t *testing.T, bin string, runner []string, snippet string) []string {
	t.Helper()
	return asmRefusals(runX86BenchDriver(t, bin, runner, snippet))
}

// checkPinnedX86 assembles the cases as one program and walks the byte
// stream case by case, naming the diverging source line. x86 instructions
// are variable-length, so each case advances the cursor by its own length.
// checkPinnedX86 compares the self-host assembler's bytes against `want`
// sequences taken from GNU as.
func checkPinnedX86(t *testing.T, bin string, runner []string, cases []pinnedX86) {
	t.Helper()
	checkPinnedX86Against(t, bin, runner, "GNU as", cases)
}

// checkPinnedX86Against is checkPinnedX86 with the oracle named, for rows
// whose `want` came from somewhere else. A failure message that names the
// wrong oracle sends the reader to the wrong tool to reproduce it.
func checkPinnedX86Against(t *testing.T, bin string, runner []string, oracle string, cases []pinnedX86) {
	t.Helper()
	var b strings.Builder
	b.WriteString(".text\n_start:\n")
	total := 0
	for _, c := range cases {
		b.WriteString("    " + c.asm + "\n")
		total += len(c.want)
	}
	got := assembleSelfHostX86(t, bin, runner, b.String())
	cur := 0
	for _, c := range cases {
		if cur+len(c.want) > len(got) {
			t.Fatalf("%q: byte stream ends early (want %d more bytes at offset %d, have %d total)", c.asm, len(c.want), cur, len(got))
		}
		for i, w := range c.want {
			if got[cur+i] != w {
				t.Errorf("%q: byte %d: self-host %02x, %s %02x (self-host % x, %s % x)",
					c.asm, i, got[cur+i], oracle, w, got[cur:cur+len(c.want)], oracle, c.want)
				break
			}
		}
		cur += len(c.want)
	}
	if cur != len(got) {
		t.Errorf("byte stream length %d, the pinned cases account for %d — an instruction assembled at the wrong length", len(got), total)
	}
}

// checkRefusedX86 feeds each line separately and requires the assembler to
// record a refusal for it.
func checkRefusedX86(t *testing.T, bin string, runner []string, lines []string) {
	t.Helper()
	for _, ln := range lines {
		if got := refusalsForX86(t, bin, runner, ".text\n_start:\n    "+ln+"\n    ret\n"); len(got) == 0 {
			t.Errorf("%q: expected a refusal, the assembler accepted it", ln)
		}
	}
}

// TestSelfHostX86MovzbWidthGas pins movzb at BOTH destination widths. The
// value a movzbl produces is identical to a movzbq's — writing a 32-bit
// register clears the upper half either way — so routing movzbl to the
// 64-bit encoder was invisible to every execution test and to the snippet
// rows, and put a REX.W in front of every byte load a program does. The
// whole-program gate caught it; these rows keep it caught. Bytes are as +
// objdump output for this exact text.
func TestSelfHostX86MovzbWidthGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)
	checkPinnedX86(t, bin, runner, []pinnedX86{
		{"movzbl %al, %eax", []byte{0x0f, 0xb6, 0xc0}},
		// spl/bpl/sil/dil need a bare REX even at 32 bits, or the number
		// names ah/ch/dh/bh instead.
		{"movzbl %spl, %esi", []byte{0x40, 0x0f, 0xb6, 0xf4}},
		{"movzbl %r9b, %r10d", []byte{0x45, 0x0f, 0xb6, 0xd1}},
		// A memory source has no byte REGISTER, so no forced REX: the reg
		// field here is the 32-bit destination.
		{"movzbl (%rax,%rcx), %esi", []byte{0x0f, 0xb6, 0x34, 0x08}},
		{"movzbl 8(%rdi), %eax", []byte{0x0f, 0xb6, 0x47, 0x08}},
		{"movzbq %al, %rax", []byte{0x48, 0x0f, 0xb6, 0xc0}},
		{"movzbq %spl, %rsi", []byte{0x48, 0x0f, 0xb6, 0xf4}},
		{"movzbq (%rax,%rcx), %rsi", []byte{0x48, 0x0f, 0xb6, 0x34, 0x08}},
	})
}

// TestSelfHostX86CarryAluGas: adc/sbb at all four widths and every ALU
// operand form, plus the 83 short-immediate selection the whole group-1
// family now shares (an imm8 previously always took the 7-byte 81 form).
// TestSelfHostX86ConvertExtendGas pins #8020: the scalar converts, movss, the
// sign-extending byte/word loads, and the flags moves and rep conditionals —
// the 13 mnemonics internal/native/x86_64 assembled and this one did not.
//
// The sign-extend rows share the trap the movzb ones already pin: a byte
// SOURCE of spl/bpl/sil/dil needs a bare REX even when nothing else does, or
// those numbers name ah/ch/dh/bh instead. A WORD source has no such
// ambiguity, so movswl must NOT force one — which is why both are here.
//
// The convert rows pin the two axes that are easy to cross: the prefix picks
// the precision (F2 double, F3 single) and the opcode picks truncating (2C)
// from rounding (2D), so getting either wrong yields a valid instruction that
// converts the wrong thing.
func TestSelfHostX86ConvertExtendGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)
	checkPinnedX86(t, bin, runner, []pinnedX86{
		{"cvtsd2sil %xmm1, %eax", []byte{0xf2, 0x0f, 0x2d, 0xc1}},
		{"cvtsd2siq %xmm1, %rax", []byte{0xf2, 0x48, 0x0f, 0x2d, 0xc1}},
		{"cvtsd2siq %xmm9, %r11", []byte{0xf2, 0x4d, 0x0f, 0x2d, 0xd9}},
		{"cvtss2sil %xmm1, %eax", []byte{0xf3, 0x0f, 0x2d, 0xc1}},
		{"cvtss2siq %xmm1, %rax", []byte{0xf3, 0x48, 0x0f, 0x2d, 0xc1}},
		{"cvttss2sil %xmm1, %eax", []byte{0xf3, 0x0f, 0x2c, 0xc1}},
		{"cvttss2siq %xmm9, %r11", []byte{0xf3, 0x4d, 0x0f, 0x2c, 0xd9}},
		// The double-precision pair already existed; they are here so the
		// generalised encoder is pinned against its own starting point.
		{"cvttsd2sil %xmm1, %eax", []byte{0xf2, 0x0f, 0x2c, 0xc1}},
		{"cvttsd2siq %xmm1, %rax", []byte{0xf2, 0x48, 0x0f, 0x2c, 0xc1}},
		{"cvtsi2ssl %eax, %xmm1", []byte{0xf3, 0x0f, 0x2a, 0xc8}},
		{"cvtsi2ssq %rax, %xmm1", []byte{0xf3, 0x48, 0x0f, 0x2a, 0xc8}},
		{"cvtsi2ssl %r9d, %xmm2", []byte{0xf3, 0x41, 0x0f, 0x2a, 0xd1}},

		{"movss %xmm1, %xmm2", []byte{0xf3, 0x0f, 0x10, 0xd1}},
		{"movss %xmm9, %xmm2", []byte{0xf3, 0x41, 0x0f, 0x10, 0xd1}},
		{"movss 8(%rdi), %xmm1", []byte{0xf3, 0x0f, 0x10, 0x4f, 0x08}},

		{"movsbl %al, %ecx", []byte{0x0f, 0xbe, 0xc8}},
		{"movsbl %spl, %esi", []byte{0x40, 0x0f, 0xbe, 0xf4}},
		{"movsbl %r9b, %r10d", []byte{0x45, 0x0f, 0xbe, 0xd1}},
		{"movsbq %al, %rcx", []byte{0x48, 0x0f, 0xbe, 0xc8}},
		{"movsbq 8(%rdi), %rax", []byte{0x48, 0x0f, 0xbe, 0x47, 0x08}},
		{"movswl %ax, %ecx", []byte{0x0f, 0xbf, 0xc8}},
		{"movswl %r9w, %r10d", []byte{0x45, 0x0f, 0xbf, 0xd1}},
		// %si / %di / %bp are word registers 4..7 — the numbers that, as
		// BYTE registers, would name ah/ch/dh/bh and so force a bare REX.
		// There is no such ambiguity at word width, so these must carry NO
		// prefix. Without these rows the force rule can be widened to every
		// width and nothing fails.
		{"movswl %si, %ecx", []byte{0x0f, 0xbf, 0xce}},
		{"movswl %bp, %eax", []byte{0x0f, 0xbf, 0xc5}},
		{"movswq %di, %rcx", []byte{0x48, 0x0f, 0xbf, 0xcf}},
		{"movswq %ax, %rcx", []byte{0x48, 0x0f, 0xbf, 0xc8}},
		{"movswq (%rax,%rcx), %rsi", []byte{0x48, 0x0f, 0xbf, 0x34, 0x08}},

		{"pushfq", []byte{0x9c}},
		{"popfq", []byte{0x9d}},
		{"ud2", []byte{0x0f, 0x0b}},
		// rep and repe are the same F3 byte; repne/repnz are F2, a different
		// prefix rather than an alias.
		{"repe cmpsb", []byte{0xf3, 0xa6}},
		{"repz cmpsb", []byte{0xf3, 0xa6}},
		{"repne scasb", []byte{0xf2, 0xae}},
		{"repnz scasb", []byte{0xf2, 0xae}},
	})
}

func TestSelfHostX86CarryAluGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)
	checkPinnedX86(t, bin, runner, []pinnedX86{
		{"adcq %rcx, %rax", []byte{0x48, 0x11, 0xc8}},
		{"adcq %r9, %r10", []byte{0x4d, 0x11, 0xca}},
		{"adcq $1, %rcx", []byte{0x48, 0x83, 0xd1, 0x01}},
		{"adcq $200, %rcx", []byte{0x48, 0x81, 0xd1, 0xc8, 0x00, 0x00, 0x00}},
		{"adcq (%rdi), %rax", []byte{0x48, 0x13, 0x07}},
		{"adcq %rax, 8(%rdi)", []byte{0x48, 0x11, 0x47, 0x08}},
		{"adcq $3, -8(%rbp)", []byte{0x48, 0x83, 0x55, 0xf8, 0x03}},
		{"adcl %ecx, %eax", []byte{0x11, 0xc8}},
		{"adcl $100000, %edx", []byte{0x81, 0xd2, 0xa0, 0x86, 0x01, 0x00}},
		{"adcw %ax, %bx", []byte{0x66, 0x11, 0xc3}},
		{"adcw $2, %cx", []byte{0x66, 0x83, 0xd1, 0x02}},
		{"adcb %al, %cl", []byte{0x10, 0xc1}},
		{"adcb $1, %dl", []byte{0x80, 0xd2, 0x01}},
		{"adcb %sil, %dil", []byte{0x40, 0x10, 0xf7}},
		{"sbbq %rcx, %rax", []byte{0x48, 0x19, 0xc8}},
		{"sbbq $1, %rcx", []byte{0x48, 0x83, 0xd9, 0x01}},
		{"sbbq (%rdi), %rax", []byte{0x48, 0x1b, 0x07}},
		{"sbbq %rax, 8(%rdi)", []byte{0x48, 0x19, 0x47, 0x08}},
		{"sbbl %ecx, %eax", []byte{0x19, 0xc8}},
		{"sbbw $700, %dx", []byte{0x66, 0x81, 0xda, 0xbc, 0x02}},
		{"sbbb $1, %bl", []byte{0x80, 0xdb, 0x01}},
		{"addq $6, %rax", []byte{0x48, 0x83, 0xc0, 0x06}},
		{"addq $6, %r9", []byte{0x49, 0x83, 0xc1, 0x06}},
		{"addq $1000, %rcx", []byte{0x48, 0x81, 0xc1, 0xe8, 0x03, 0x00, 0x00}},
		{"addq $128, %rdx", []byte{0x48, 0x81, 0xc2, 0x80, 0x00, 0x00, 0x00}},
		{"addq $-128, %rax", []byte{0x48, 0x83, 0xc0, 0x80}},
		{"cmpq $-1, %rax", []byte{0x48, 0x83, 0xf8, 0xff}},
		{"addw %ax, %bx", []byte{0x66, 0x01, 0xc3}},
		{"addb %al, (%rsi)", []byte{0x00, 0x06}},
		{"addb (%rsi), %al", []byte{0x02, 0x06}},
		{"andw $15, %dx", []byte{0x66, 0x83, 0xe2, 0x0f}},
		{"orw %dx, (%rdi)", []byte{0x66, 0x09, 0x17}},
		{"xorb $8, %cl", []byte{0x80, 0xf1, 0x08}},
		{"cmpw $9, 4(%rsp)", []byte{0x66, 0x83, 0x7c, 0x24, 0x04, 0x09}},
		{"cmpb $61, (%rdi)", []byte{0x80, 0x3f, 0x3d}},
		{"subb %cl, %dl", []byte{0x28, 0xca}},
		{"orq $4096, %r8", []byte{0x49, 0x81, 0xc8, 0x00, 0x10, 0x00, 0x00}},
		{"andl $15, (%rdx)", []byte{0x83, 0x22, 0x0f}},
		{"xorw %r9w, %r10w", []byte{0x66, 0x45, 0x31, 0xca}},
	})
	// Width mismatches must refuse, not encode at the wrong width.
	checkRefusedX86(t, bin, runner, []string{
		"addq %eax, %rbx",
		"adcq $1, %ecx",
		"addw %eax, %ax",
		"adcb $1, %ax",
		"movw %ax, %bx",
	})
}

// TestSelfHostX86UnaryIncDecTestGas: the F6/F7 one-operand group (not/neg/
// mul/one-operand imul/div/idiv), the two- and three-operand imul, the
// FE/FF inc/dec matrix, and test with memory operands both directions and
// mem,imm — F6/F7 selection by suffix, 66 prefix for the w forms.
func TestSelfHostX86UnaryIncDecTestGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)
	checkPinnedX86(t, bin, runner, []pinnedX86{
		{"notq %rax", []byte{0x48, 0xf7, 0xd0}},
		{"notq %r9", []byte{0x49, 0xf7, 0xd1}},
		{"notl %ecx", []byte{0xf7, 0xd1}},
		{"notw %dx", []byte{0x66, 0xf7, 0xd2}},
		{"notb %al", []byte{0xf6, 0xd0}},
		{"notq (%rdi)", []byte{0x48, 0xf7, 0x17}},
		{"notw (%rdi)", []byte{0x66, 0xf7, 0x17}},
		{"notb 3(%rsi)", []byte{0xf6, 0x56, 0x03}},
		{"negb %cl", []byte{0xf6, 0xd9}},
		{"negw %ax", []byte{0x66, 0xf7, 0xd8}},
		{"negl %r9d", []byte{0x41, 0xf7, 0xd9}},
		{"negq -8(%rbp)", []byte{0x48, 0xf7, 0x5d, 0xf8}},
		{"mulq %rcx", []byte{0x48, 0xf7, 0xe1}},
		{"mull %esi", []byte{0xf7, 0xe6}},
		{"mulw %cx", []byte{0x66, 0xf7, 0xe1}},
		{"mulb %dl", []byte{0xf6, 0xe2}},
		{"mulq (%rdi)", []byte{0x48, 0xf7, 0x27}},
		{"imulq %rcx", []byte{0x48, 0xf7, 0xe9}},
		{"imull %ecx", []byte{0xf7, 0xe9}},
		{"imulw %cx", []byte{0x66, 0xf7, 0xe9}},
		{"imulb %dl", []byte{0xf6, 0xea}},
		{"imull %ecx, %eax", []byte{0x0f, 0xaf, 0xc1}},
		{"imulq (%rdi), %rax", []byte{0x48, 0x0f, 0xaf, 0x07}},
		{"imulw %cx, %dx", []byte{0x66, 0x0f, 0xaf, 0xd1}},
		{"imulq $20, %rcx, %rax", []byte{0x48, 0x6b, 0xc1, 0x14}},
		{"imulq $2000, %rcx, %rax", []byte{0x48, 0x69, 0xc1, 0xd0, 0x07, 0x00, 0x00}},
		{"imull $-3, %edx, %ecx", []byte{0x6b, 0xca, 0xfd}},
		{"divb %cl", []byte{0xf6, 0xf1}},
		{"divw %cx", []byte{0x66, 0xf7, 0xf1}},
		{"divl %esi", []byte{0xf7, 0xf6}},
		{"divq (%rdi)", []byte{0x48, 0xf7, 0x37}},
		{"idivb %cl", []byte{0xf6, 0xf9}},
		{"idivw %cx", []byte{0x66, 0xf7, 0xf9}},
		{"idivl %esi", []byte{0xf7, 0xfe}},
		{"idivl (%rdx)", []byte{0xf7, 0x3a}},
		{"incb %al", []byte{0xfe, 0xc0}},
		{"incw %cx", []byte{0x66, 0xff, 0xc1}},
		{"incl %edx", []byte{0xff, 0xc2}},
		{"incq %r10", []byte{0x49, 0xff, 0xc2}},
		{"incb (%rdi)", []byte{0xfe, 0x07}},
		{"incw 2(%rsi)", []byte{0x66, 0xff, 0x46, 0x02}},
		{"incl -4(%rbp)", []byte{0xff, 0x45, 0xfc}},
		{"incq (%r9)", []byte{0x49, 0xff, 0x01}},
		{"decb %cl", []byte{0xfe, 0xc9}},
		{"decw %dx", []byte{0x66, 0xff, 0xca}},
		{"decl %r11d", []byte{0x41, 0xff, 0xcb}},
		{"decq %rbx", []byte{0x48, 0xff, 0xcb}},
		{"decl 8(%rsp)", []byte{0xff, 0x4c, 0x24, 0x08}},
		{"decq (%rdi)", []byte{0x48, 0xff, 0x0f}},
		{"testq %rax, (%rdi)", []byte{0x48, 0x85, 0x07}},
		{"testq (%rdi), %rax", []byte{0x48, 0x85, 0x07}},
		{"testl %ecx, 4(%rsi)", []byte{0x85, 0x4e, 0x04}},
		{"testw %dx, (%rbx)", []byte{0x66, 0x85, 0x13}},
		{"testb %al, (%rcx)", []byte{0x84, 0x01}},
		{"testq $255, (%rdi)", []byte{0x48, 0xf7, 0x07, 0xff, 0x00, 0x00, 0x00}},
		{"testl $15, -4(%rbp)", []byte{0xf7, 0x45, 0xfc, 0x0f, 0x00, 0x00, 0x00}},
		{"testw $3, (%rsi)", []byte{0x66, 0xf7, 0x06, 0x03, 0x00}},
		{"testb $1, 2(%rdx)", []byte{0xf6, 0x42, 0x02, 0x01}},
		{"testw %ax, %cx", []byte{0x66, 0x85, 0xc1}},
		{"testb %sil, %dil", []byte{0x40, 0x84, 0xf7}},
	})
	checkRefusedX86(t, bin, runner, []string{
		"notq %ecx",
		"mulw %eax",
		"imulb %cl, %al",
		"incq %eax",
		"testl $1, %rax",
	})
}

// TestSelfHostX86ShiftBtCmovGas: all seven shift/rotate ops at all widths
// (imm, the $1 short form, %cl; register and memory destinations),
// shld/shrd, the bt/bts/btr/btc group in AT&T operand order, bswap, and
// the full 30-alias cmovcc table at W/L/W16 widths with memory sources.
func TestSelfHostX86ShiftBtCmovGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)
	checkPinnedX86(t, bin, runner, []pinnedX86{
		{"rolq $3, %rax", []byte{0x48, 0xc1, 0xc0, 0x03}},
		{"rolq %cl, %r9", []byte{0x49, 0xd3, 0xc1}},
		{"roll $1, %ecx", []byte{0xd1, 0xc1}},
		{"rolw $2, %dx", []byte{0x66, 0xc1, 0xc2, 0x02}},
		{"rolb $7, %al", []byte{0xc0, 0xc0, 0x07}},
		{"rorq $3, %rax", []byte{0x48, 0xc1, 0xc8, 0x03}},
		{"rorl %cl, %esi", []byte{0xd3, 0xce}},
		{"rorb $1, %cl", []byte{0xd0, 0xc9}},
		{"rclq $2, %rcx", []byte{0x48, 0xc1, 0xd1, 0x02}},
		{"rcll %cl, %eax", []byte{0xd3, 0xd0}},
		{"rcrq $1, %rdx", []byte{0x48, 0xd1, 0xda}},
		{"rcrb $3, %dl", []byte{0xc0, 0xda, 0x03}},
		{"shlb $3, %cl", []byte{0xc0, 0xe1, 0x03}},
		{"shlw %cl, %ax", []byte{0x66, 0xd3, 0xe0}},
		{"shll $1, %r8d", []byte{0x41, 0xd1, 0xe0}},
		{"shlq $1, %rax", []byte{0x48, 0xd1, 0xe0}},
		{"shrb %cl, %bl", []byte{0xd2, 0xeb}},
		{"shrw $5, %cx", []byte{0x66, 0xc1, 0xe9, 0x05}},
		{"shrl %cl, %r10d", []byte{0x41, 0xd3, 0xea}},
		{"sarb $1, %al", []byte{0xd0, 0xf8}},
		{"sarw $3, %dx", []byte{0x66, 0xc1, 0xfa, 0x03}},
		{"sarl $2, %r9d", []byte{0x41, 0xc1, 0xf9, 0x02}},
		{"shlq $2, (%rdi)", []byte{0x48, 0xc1, 0x27, 0x02}},
		{"shrl $1, 4(%rsi)", []byte{0xd1, 0x6e, 0x04}},
		{"rolw %cl, (%rdx)", []byte{0x66, 0xd3, 0x02}},
		{"sarq %cl, -8(%rbp)", []byte{0x48, 0xd3, 0x7d, 0xf8}},
		{"rcrl $1, (%rcx)", []byte{0xd1, 0x19}},
		{"shldq $5, %rsi, %rdi", []byte{0x48, 0x0f, 0xa4, 0xf7, 0x05}},
		{"shldq %cl, %rsi, %rdi", []byte{0x48, 0x0f, 0xa5, 0xf7}},
		{"shldl $3, %ecx, %edx", []byte{0x0f, 0xa4, 0xca, 0x03}},
		{"shldw $2, %ax, %cx", []byte{0x66, 0x0f, 0xa4, 0xc1, 0x02}},
		{"shldq $5, %r9, (%rdi)", []byte{0x4c, 0x0f, 0xa4, 0x0f, 0x05}},
		{"shrdq $5, %rsi, %rdi", []byte{0x48, 0x0f, 0xac, 0xf7, 0x05}},
		{"shrdq %cl, %r9, %r10", []byte{0x4d, 0x0f, 0xad, 0xca}},
		{"shrdl %cl, %edx, %eax", []byte{0x0f, 0xad, 0xd0}},
		{"shrdq %cl, %rax, 8(%rsp)", []byte{0x48, 0x0f, 0xad, 0x44, 0x24, 0x08}},
		{"btq %rcx, %rax", []byte{0x48, 0x0f, 0xa3, 0xc8}},
		{"btq $3, %rax", []byte{0x48, 0x0f, 0xba, 0xe0, 0x03}},
		{"btl %ecx, %edx", []byte{0x0f, 0xa3, 0xca}},
		{"btl $31, %esi", []byte{0x0f, 0xba, 0xe6, 0x1f}},
		{"btw %ax, %cx", []byte{0x66, 0x0f, 0xa3, 0xc1}},
		{"btq %r9, (%rdi)", []byte{0x4c, 0x0f, 0xa3, 0x0f}},
		{"btq $17, 8(%rsi)", []byte{0x48, 0x0f, 0xba, 0x66, 0x08, 0x11}},
		{"btsq %rcx, %rax", []byte{0x48, 0x0f, 0xab, 0xc8}},
		{"btsq $3, (%rdi)", []byte{0x48, 0x0f, 0xba, 0x2f, 0x03}},
		{"btsl %eax, 4(%rsi)", []byte{0x0f, 0xab, 0x46, 0x04}},
		{"btrq %rdx, %rcx", []byte{0x48, 0x0f, 0xb3, 0xd1}},
		{"btrl $5, %r9d", []byte{0x41, 0x0f, 0xba, 0xf1, 0x05}},
		{"btcq %rax, (%rdx)", []byte{0x48, 0x0f, 0xbb, 0x02}},
		{"btcw $2, %cx", []byte{0x66, 0x0f, 0xba, 0xf9, 0x02}},
		{"bswapq %rax", []byte{0x48, 0x0f, 0xc8}},
		{"bswapq %r9", []byte{0x49, 0x0f, 0xc9}},
		{"bswapl %ecx", []byte{0x0f, 0xc9}},
		{"bswapl %r10d", []byte{0x41, 0x0f, 0xca}},
		{"cmovo %rcx, %rax", []byte{0x48, 0x0f, 0x40, 0xc1}},
		{"cmovno %rcx, %rax", []byte{0x48, 0x0f, 0x41, 0xc1}},
		{"cmovnae %ecx, %eax", []byte{0x0f, 0x42, 0xc1}},
		{"cmovnb %rdx, %rcx", []byte{0x48, 0x0f, 0x43, 0xca}},
		{"cmovna %r9, %r10", []byte{0x4d, 0x0f, 0x46, 0xd1}},
		{"cmovnbe %rax, %rbx", []byte{0x48, 0x0f, 0x47, 0xd8}},
		{"cmovp %rcx, %rdx", []byte{0x48, 0x0f, 0x4a, 0xd1}},
		{"cmovnp %rcx, %rdx", []byte{0x48, 0x0f, 0x4b, 0xd1}},
		{"cmovnge %ecx, %edx", []byte{0x0f, 0x4c, 0xd1}},
		{"cmovnl %ecx, %edx", []byte{0x0f, 0x4d, 0xd1}},
		{"cmovng %rsi, %rdi", []byte{0x48, 0x0f, 0x4e, 0xfe}},
		{"cmovnle %rsi, %rdi", []byte{0x48, 0x0f, 0x4f, 0xfe}},
		{"cmovsq %r8, %r9", []byte{0x4d, 0x0f, 0x48, 0xc8}},
		{"cmovnsl %eax, %ecx", []byte{0x0f, 0x49, 0xc8}},
		{"cmovew %ax, %cx", []byte{0x66, 0x0f, 0x44, 0xc8}},
		{"cmovzl %r11d, %r12d", []byte{0x45, 0x0f, 0x44, 0xe3}},
		{"cmoveq 8(%rdi), %rax", []byte{0x48, 0x0f, 0x44, 0x47, 0x08}},
		{"cmovc %rax, %rcx", []byte{0x48, 0x0f, 0x42, 0xc8}},
		{"cmovnc %rax, %rcx", []byte{0x48, 0x0f, 0x43, 0xc8}},
	})
	checkRefusedX86(t, bin, runner, []string{
		"btb $1, %al",
		"shldb $1, %al, %cl",
		"bswapw %ax",
		"bswapb %al",
		"shlq %rbx, %rax",
		"cmovneb %al, %cl",
	})
}

// TestSelfHostX86AtomicsStringMiscGas: xadd/cmpxchg at all widths, the
// validated lock prefix, xchg with memory, push imm8/imm32 and push/pop
// memory, indirect call/jmp through registers and SIB memory, the cbtw
// sign-extend family, the bare and rep-prefixed string ops, the fixed
// misc set (std/nop/int3/leave/pause/fences), and the movb register forms
// that previously misencoded as stores through %rdi.
func TestSelfHostX86AtomicsStringMiscGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)
	checkPinnedX86(t, bin, runner, []pinnedX86{
		{"xaddq %rax, (%rdi)", []byte{0x48, 0x0f, 0xc1, 0x07}},
		{"xaddq %r9, %rcx", []byte{0x4c, 0x0f, 0xc1, 0xc9}},
		{"xaddl %eax, 4(%rsi)", []byte{0x0f, 0xc1, 0x46, 0x04}},
		{"xaddw %cx, (%rdx)", []byte{0x66, 0x0f, 0xc1, 0x0a}},
		{"xaddb %al, (%rbx)", []byte{0x0f, 0xc0, 0x03}},
		{"cmpxchgq %rcx, (%rdi)", []byte{0x48, 0x0f, 0xb1, 0x0f}},
		{"cmpxchgq %r9, %rax", []byte{0x4c, 0x0f, 0xb1, 0xc8}},
		{"cmpxchgl %edx, -8(%rbp)", []byte{0x0f, 0xb1, 0x55, 0xf8}},
		{"cmpxchgw %cx, (%rsi)", []byte{0x66, 0x0f, 0xb1, 0x0e}},
		{"cmpxchgb %cl, (%rdx)", []byte{0x0f, 0xb0, 0x0a}},
		{"lock addq $1, (%rdi)", []byte{0xf0, 0x48, 0x83, 0x07, 0x01}},
		{"lock incl (%rsi)", []byte{0xf0, 0xff, 0x06}},
		{"lock xaddq %rax, (%rdi)", []byte{0xf0, 0x48, 0x0f, 0xc1, 0x07}},
		{"lock cmpxchgq %rcx, (%rdi)", []byte{0xf0, 0x48, 0x0f, 0xb1, 0x0f}},
		{"lock btsq $3, (%rdi)", []byte{0xf0, 0x48, 0x0f, 0xba, 0x2f, 0x03}},
		{"xchgq %rcx, (%rdi)", []byte{0x48, 0x87, 0x0f}},
		{"xchgq (%rdi), %rcx", []byte{0x48, 0x87, 0x0f}},
		{"xchgq %rcx, %rdx", []byte{0x48, 0x87, 0xca}},
		{"xchgl %esi, (%r8)", []byte{0x41, 0x87, 0x30}},
		{"xchgw %dx, (%rcx)", []byte{0x66, 0x87, 0x11}},
		{"xchgb %cl, (%rsi)", []byte{0x86, 0x0e}},
		{"xchgb %dl, %cl", []byte{0x86, 0xd1}},
		{"pushq $1", []byte{0x6a, 0x01}},
		{"pushq $-1", []byte{0x6a, 0xff}},
		{"pushq $300", []byte{0x68, 0x2c, 0x01, 0x00, 0x00}},
		{"pushq $-300", []byte{0x68, 0xd4, 0xfe, 0xff, 0xff}},
		{"pushq 8(%rdi)", []byte{0xff, 0x77, 0x08}},
		{"pushq (%r9)", []byte{0x41, 0xff, 0x31}},
		{"popq -8(%rbp)", []byte{0x8f, 0x45, 0xf8}},
		{"popq (%r10)", []byte{0x41, 0x8f, 0x02}},
		{"call *%rax", []byte{0xff, 0xd0}},
		{"call *8(%rdi)", []byte{0xff, 0x57, 0x08}},
		{"call *(%rax,%rcx,8)", []byte{0xff, 0x14, 0xc8}},
		{"jmp *%rdx", []byte{0xff, 0xe2}},
		{"jmp *16(%rsi)", []byte{0xff, 0x66, 0x10}},
		{"jmp *(%rax,%rcx,8)", []byte{0xff, 0x24, 0xc8}},
		{"cbtw", []byte{0x66, 0x98}},
		{"cwtl", []byte{0x98}},
		{"cltq", []byte{0x48, 0x98}},
		{"cwtd", []byte{0x66, 0x99}},
		{"cltd", []byte{0x99}},
		{"cqto", []byte{0x48, 0x99}},
		{"std", []byte{0xfd}},
		{"nop", []byte{0x90}},
		{"int3", []byte{0xcc}},
		{"leave", []byte{0xc9}},
		{"pause", []byte{0xf3, 0x90}},
		{"mfence", []byte{0x0f, 0xae, 0xf0}},
		{"lfence", []byte{0x0f, 0xae, 0xe8}},
		{"sfence", []byte{0x0f, 0xae, 0xf8}},
		{"movsb", []byte{0xa4}},
		{"movsw", []byte{0x66, 0xa5}},
		{"movsl", []byte{0xa5}},
		{"movsq", []byte{0x48, 0xa5}},
		{"stosb", []byte{0xaa}},
		{"stosw", []byte{0x66, 0xab}},
		{"stosl", []byte{0xab}},
		{"stosq", []byte{0x48, 0xab}},
		{"lodsb", []byte{0xac}},
		{"lodsw", []byte{0x66, 0xad}},
		{"lodsl", []byte{0xad}},
		{"lodsq", []byte{0x48, 0xad}},
		{"scasb", []byte{0xae}},
		{"scasw", []byte{0x66, 0xaf}},
		{"scasl", []byte{0xaf}},
		{"scasq", []byte{0x48, 0xaf}},
		{"cmpsb", []byte{0xa6}},
		{"cmpsw", []byte{0x66, 0xa7}},
		{"cmpsl", []byte{0xa7}},
		{"cmpsq", []byte{0x48, 0xa7}},
		{"rep stosb", []byte{0xf3, 0xaa}},
		{"rep lodsq", []byte{0xf3, 0x48, 0xad}},
		{"rep scasb", []byte{0xf3, 0xae}},
		{"rep cmpsl", []byte{0xf3, 0xa7}},
		{"rep movsl", []byte{0xf3, 0xa5}},
		// The operand-size prefix goes BEFORE the repeat prefix. gas
		// assembles `rep movsw` as 66 F3 A5; emitting F3 66 A5 decodes to
		// the same instruction, so only a byte-level oracle sees it — and
		// every rep case pinned here before #7903 phase 3 happened to be a
		// width with no 66. A REX is NOT moved: `rep movsq` stays F3 48 A5.
		{"rep movsw", []byte{0x66, 0xf3, 0xa5}},
		{"rep stosw", []byte{0x66, 0xf3, 0xab}},
		{"rep scasw", []byte{0x66, 0xf3, 0xaf}},
		{"rep cmpsw", []byte{0x66, 0xf3, 0xa7}},
		{"repne scasw", []byte{0x66, 0xf2, 0xaf}},
		{"rep movsq", []byte{0xf3, 0x48, 0xa5}},
		// The two non-string idioms gas takes after rep: `rep ret` is the
		// AMD branch-prediction workaround and `rep nop` IS pause.
		{"rep ret", []byte{0xf3, 0xc3}},
		{"rep nop", []byte{0xf3, 0x90}},
		// The Intel spellings gas accepts in AT&T syntax too — one
		// instruction under two names, which is how eight of these went
		// missing from one assembler without any byte test noticing.
		{"cbw", []byte{0x66, 0x98}},
		{"cwde", []byte{0x98}},
		{"cdqe", []byte{0x48, 0x98}},
		{"cwd", []byte{0x66, 0x99}},
		{"cdq", []byte{0x99}},
		{"cqo", []byte{0x48, 0x99}},
		{"movsd", []byte{0xa5}},
		{"cmpsd", []byte{0xa7}},
		{"pushf", []byte{0x9c}},
		{"popf", []byte{0x9d}},
		{"movb $1, %cl", []byte{0xb1, 0x01}},
		{"movb $255, %sil", []byte{0x40, 0xb6, 0xff}},
		{"movb %al, %bl", []byte{0x88, 0xc3}},
		{"movb %r9b, %dil", []byte{0x44, 0x88, 0xcf}},
	})
	checkRefusedX86(t, bin, runner, []string{
		"lock movq $1, (%rdi)",
		"lock btq $1, (%rdi)",
		"rep frob",
		// gas: "invalid instruction `leave' after `rep'". The prefix is not
		// a no-op in front of these — it is refused, so accepting it would
		// emit a byte the CPU reads as part of an instruction it does not
		// belong to.
		"rep leave",
		"rep cld",
		"repne addq $1, %rax",
		// The dword string ops are spelled with `l` in AT&T; the `d`
		// spellings are Intel-only and gas rejects them here.
		"stosd",
		"lodsd",
		"scasd",
		"xchgq $1, %rax",
		"pushw %ax",
	})
}

// TestSelfHostX86SsePackedGas: the movdqu/movdqa store direction (the load
// was the only encoded one), movups/movupd, the whole packed-integer
// table, and the vector shifts in both the by-register and 0F 71/72/73
// by-immediate forms.
func TestSelfHostX86SsePackedGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)
	checkPinnedX86(t, bin, runner, []pinnedX86{
		{"movdqa %xmm1, %xmm2", []byte{0x66, 0x0f, 0x6f, 0xd1}},
		{"movdqa (%rdi), %xmm3", []byte{0x66, 0x0f, 0x6f, 0x1f}},
		{"movdqa %xmm3, (%rdi)", []byte{0x66, 0x0f, 0x7f, 0x1f}},
		{"movdqu %xmm1, %xmm2", []byte{0xf3, 0x0f, 0x6f, 0xd1}},
		{"movdqu (%rax,%rcx,2), %xmm8", []byte{0xf3, 0x44, 0x0f, 0x6f, 0x04, 0x48}},
		{"movdqu %xmm9, 16(%rdi)", []byte{0xf3, 0x44, 0x0f, 0x7f, 0x4f, 0x10}},
		{"movups (%rdi), %xmm1", []byte{0x0f, 0x10, 0x0f}},
		{"movups %xmm1, (%rdi)", []byte{0x0f, 0x11, 0x0f}},
		{"movups %xmm2, %xmm3", []byte{0x0f, 0x10, 0xda}},
		{"movupd (%rsi), %xmm4", []byte{0x66, 0x0f, 0x10, 0x26}},
		{"movupd %xmm4, 8(%rsi)", []byte{0x66, 0x0f, 0x11, 0x66, 0x08}},
		{"movsd %xmm3, (%rdi)", []byte{0xf2, 0x0f, 0x11, 0x1f}},
		{"movsd (%rdi), %xmm3", []byte{0xf2, 0x0f, 0x10, 0x1f}},
		{"movq %xmm1, %xmm2", []byte{0xf3, 0x0f, 0x7e, 0xd1}},
		{"paddb %xmm1, %xmm2", []byte{0x66, 0x0f, 0xfc, 0xd1}},
		{"paddw %xmm8, %xmm9", []byte{0x66, 0x45, 0x0f, 0xfd, 0xc8}},
		{"paddd (%rdi), %xmm1", []byte{0x66, 0x0f, 0xfe, 0x0f}},
		{"paddq %xmm0, %xmm1", []byte{0x66, 0x0f, 0xd4, 0xc8}},
		{"psubb %xmm1, %xmm0", []byte{0x66, 0x0f, 0xf8, 0xc1}},
		{"psubw %xmm2, %xmm3", []byte{0x66, 0x0f, 0xf9, 0xda}},
		{"psubd %xmm4, %xmm5", []byte{0x66, 0x0f, 0xfa, 0xec}},
		{"psubq %xmm6, %xmm7", []byte{0x66, 0x0f, 0xfb, 0xfe}},
		{"paddusb %xmm1, %xmm2", []byte{0x66, 0x0f, 0xdc, 0xd1}},
		{"psubusb %xmm1, %xmm2", []byte{0x66, 0x0f, 0xd8, 0xd1}},
		{"paddsb %xmm1, %xmm2", []byte{0x66, 0x0f, 0xec, 0xd1}},
		{"psubsb %xmm1, %xmm2", []byte{0x66, 0x0f, 0xe8, 0xd1}},
		{"pavgb %xmm3, %xmm4", []byte{0x66, 0x0f, 0xe0, 0xe3}},
		{"pminub %xmm3, %xmm4", []byte{0x66, 0x0f, 0xda, 0xe3}},
		{"pmaxub %xmm3, %xmm4", []byte{0x66, 0x0f, 0xde, 0xe3}},
		{"pminsw %xmm5, %xmm6", []byte{0x66, 0x0f, 0xea, 0xf5}},
		{"pmaxsw %xmm5, %xmm6", []byte{0x66, 0x0f, 0xee, 0xf5}},
		{"pmullw %xmm1, %xmm2", []byte{0x66, 0x0f, 0xd5, 0xd1}},
		{"pmulhw %xmm1, %xmm2", []byte{0x66, 0x0f, 0xe5, 0xd1}},
		{"pmulhuw %xmm1, %xmm2", []byte{0x66, 0x0f, 0xe4, 0xd1}},
		{"pmuludq %xmm1, %xmm2", []byte{0x66, 0x0f, 0xf4, 0xd1}},
		{"psadbw %xmm1, %xmm2", []byte{0x66, 0x0f, 0xf6, 0xd1}},
		{"pand %xmm1, %xmm2", []byte{0x66, 0x0f, 0xdb, 0xd1}},
		{"pandn %xmm1, %xmm2", []byte{0x66, 0x0f, 0xdf, 0xd1}},
		{"por %xmm1, %xmm2", []byte{0x66, 0x0f, 0xeb, 0xd1}},
		{"pxor %xmm1, %xmm1", []byte{0x66, 0x0f, 0xef, 0xc9}},
		{"pcmpeqb %xmm1, %xmm0", []byte{0x66, 0x0f, 0x74, 0xc1}},
		{"pcmpeqw %xmm8, %xmm9", []byte{0x66, 0x45, 0x0f, 0x75, 0xc8}},
		{"pcmpeqd %xmm2, %xmm3", []byte{0x66, 0x0f, 0x76, 0xda}},
		{"pcmpgtb %xmm1, %xmm0", []byte{0x66, 0x0f, 0x64, 0xc1}},
		{"pcmpgtw %xmm1, %xmm0", []byte{0x66, 0x0f, 0x65, 0xc1}},
		{"pcmpgtd %xmm1, %xmm0", []byte{0x66, 0x0f, 0x66, 0xc1}},
		{"packsswb %xmm1, %xmm2", []byte{0x66, 0x0f, 0x63, 0xd1}},
		{"packuswb %xmm1, %xmm2", []byte{0x66, 0x0f, 0x67, 0xd1}},
		{"packssdw %xmm1, %xmm2", []byte{0x66, 0x0f, 0x6b, 0xd1}},
		{"punpcklbw %xmm1, %xmm1", []byte{0x66, 0x0f, 0x60, 0xc9}},
		{"punpcklwd %xmm1, %xmm1", []byte{0x66, 0x0f, 0x61, 0xc9}},
		{"punpckldq %xmm2, %xmm3", []byte{0x66, 0x0f, 0x62, 0xda}},
		{"punpcklqdq %xmm2, %xmm3", []byte{0x66, 0x0f, 0x6c, 0xda}},
		{"punpckhbw %xmm4, %xmm5", []byte{0x66, 0x0f, 0x68, 0xec}},
		{"punpckhwd %xmm4, %xmm5", []byte{0x66, 0x0f, 0x69, 0xec}},
		{"punpckhdq %xmm6, %xmm7", []byte{0x66, 0x0f, 0x6a, 0xfe}},
		{"punpckhqdq %xmm6, %xmm7", []byte{0x66, 0x0f, 0x6d, 0xfe}},
		{"psllw %xmm1, %xmm2", []byte{0x66, 0x0f, 0xf1, 0xd1}},
		{"pslld %xmm1, %xmm2", []byte{0x66, 0x0f, 0xf2, 0xd1}},
		{"psllq %xmm1, %xmm2", []byte{0x66, 0x0f, 0xf3, 0xd1}},
		{"psrlw %xmm1, %xmm2", []byte{0x66, 0x0f, 0xd1, 0xd1}},
		{"psrld %xmm1, %xmm2", []byte{0x66, 0x0f, 0xd2, 0xd1}},
		{"psrlq %xmm1, %xmm2", []byte{0x66, 0x0f, 0xd3, 0xd1}},
		{"psraw %xmm1, %xmm2", []byte{0x66, 0x0f, 0xe1, 0xd1}},
		{"psrad %xmm1, %xmm2", []byte{0x66, 0x0f, 0xe2, 0xd1}},
		{"psllw $3, %xmm2", []byte{0x66, 0x0f, 0x71, 0xf2, 0x03}},
		{"pslld $5, %xmm9", []byte{0x66, 0x41, 0x0f, 0x72, 0xf1, 0x05}},
		{"psllq $7, %xmm2", []byte{0x66, 0x0f, 0x73, 0xf2, 0x07}},
		{"psrlw $1, %xmm2", []byte{0x66, 0x0f, 0x71, 0xd2, 0x01}},
		{"psrld $2, %xmm2", []byte{0x66, 0x0f, 0x72, 0xd2, 0x02}},
		{"psrlq $63, %xmm2", []byte{0x66, 0x0f, 0x73, 0xd2, 0x3f}},
		{"psraw $4, %xmm2", []byte{0x66, 0x0f, 0x71, 0xe2, 0x04}},
		{"psrad $8, %xmm2", []byte{0x66, 0x0f, 0x72, 0xe2, 0x08}},
		{"pslldq $8, %xmm1", []byte{0x66, 0x0f, 0x73, 0xf9, 0x08}},
		{"psrldq $4, %xmm2", []byte{0x66, 0x0f, 0x73, 0xda, 0x04}},
	})
	// pslldq/psrldq exist only with an immediate count.
	checkRefusedX86(t, bin, runner, []string{
		"pslldq %xmm1, %xmm2",
		"psrldq %xmm1, %xmm2",
	})
}

// TestSelfHostX86SseFloatExtGas: packed pd/ps arithmetic, the ss scalars,
// the cvtdq2pd family, round/pcmp*stri, the 0F 38 min/max group + pmulld +
// ptest, the shuffles, pextr/pinsr (register and memory, the C5/C4 legacy
// forms where GNU as picks them), crc32 at every width, and the sign-bit
// gathers.
func TestSelfHostX86SseFloatExtGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)
	checkPinnedX86(t, bin, runner, []pinnedX86{
		{"addpd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x58, 0xd1}},
		{"subpd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x5c, 0xd1}},
		{"mulpd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x59, 0xd1}},
		{"divpd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x5e, 0xd1}},
		{"sqrtpd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x51, 0xd1}},
		{"minpd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x5d, 0xd1}},
		{"maxpd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x5f, 0xd1}},
		{"addps %xmm3, %xmm4", []byte{0x0f, 0x58, 0xe3}},
		{"subps %xmm3, %xmm4", []byte{0x0f, 0x5c, 0xe3}},
		{"mulps %xmm3, %xmm4", []byte{0x0f, 0x59, 0xe3}},
		{"divps %xmm3, %xmm4", []byte{0x0f, 0x5e, 0xe3}},
		{"sqrtps %xmm3, %xmm4", []byte{0x0f, 0x51, 0xe3}},
		{"minps %xmm3, %xmm4", []byte{0x0f, 0x5d, 0xe3}},
		{"maxps %xmm3, %xmm4", []byte{0x0f, 0x5f, 0xe3}},
		{"addss %xmm5, %xmm6", []byte{0xf3, 0x0f, 0x58, 0xf5}},
		{"subss %xmm5, %xmm6", []byte{0xf3, 0x0f, 0x5c, 0xf5}},
		{"mulss %xmm5, %xmm6", []byte{0xf3, 0x0f, 0x59, 0xf5}},
		{"divss %xmm5, %xmm6", []byte{0xf3, 0x0f, 0x5e, 0xf5}},
		{"sqrtss %xmm5, %xmm6", []byte{0xf3, 0x0f, 0x51, 0xf5}},
		{"minss %xmm5, %xmm6", []byte{0xf3, 0x0f, 0x5d, 0xf5}},
		{"maxss %xmm5, %xmm6", []byte{0xf3, 0x0f, 0x5f, 0xf5}},
		{"andpd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x54, 0xd1}},
		{"andnpd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x55, 0xd1}},
		{"orpd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x56, 0xd1}},
		{"andps %xmm1, %xmm2", []byte{0x0f, 0x54, 0xd1}},
		{"andnps %xmm1, %xmm2", []byte{0x0f, 0x55, 0xd1}},
		{"orps %xmm1, %xmm2", []byte{0x0f, 0x56, 0xd1}},
		{"xorps %xmm1, %xmm1", []byte{0x0f, 0x57, 0xc9}},
		{"unpcklpd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x14, 0xd1}},
		{"unpckhpd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x15, 0xd1}},
		{"movaps %xmm1, %xmm2", []byte{0x0f, 0x28, 0xd1}},
		{"movapd %xmm8, %xmm9", []byte{0x66, 0x45, 0x0f, 0x28, 0xc8}},
		{"comisd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x2f, 0xd1}},
		{"ucomiss %xmm1, %xmm2", []byte{0x0f, 0x2e, 0xd1}},
		{"comiss %xmm1, %xmm2", []byte{0x0f, 0x2f, 0xd1}},
		{"cvtdq2ps %xmm1, %xmm2", []byte{0x0f, 0x5b, 0xd1}},
		{"cvtps2dq %xmm1, %xmm2", []byte{0x66, 0x0f, 0x5b, 0xd1}},
		{"cvttps2dq %xmm1, %xmm2", []byte{0xf3, 0x0f, 0x5b, 0xd1}},
		{"cvtdq2pd %xmm1, %xmm2", []byte{0xf3, 0x0f, 0xe6, 0xd1}},
		{"cvtpd2dq %xmm1, %xmm2", []byte{0xf2, 0x0f, 0xe6, 0xd1}},
		{"cvttpd2dq %xmm1, %xmm2", []byte{0x66, 0x0f, 0xe6, 0xd1}},
		{"cvtdq2pd (%rdi), %xmm3", []byte{0xf3, 0x0f, 0xe6, 0x1f}},
		{"roundss $1, %xmm1, %xmm2", []byte{0x66, 0x0f, 0x3a, 0x0a, 0xd1, 0x01}},
		{"roundsd $2, %xmm8, %xmm9", []byte{0x66, 0x45, 0x0f, 0x3a, 0x0b, 0xc8, 0x02}},
		{"pcmpistri $0, %xmm1, %xmm2", []byte{0x66, 0x0f, 0x3a, 0x63, 0xd1, 0x00}},
		{"pcmpestri $1, %xmm2, %xmm3", []byte{0x66, 0x0f, 0x3a, 0x61, 0xda, 0x01}},
		{"ptest %xmm1, %xmm2", []byte{0x66, 0x0f, 0x38, 0x17, 0xd1}},
		{"pmulld %xmm1, %xmm2", []byte{0x66, 0x0f, 0x38, 0x40, 0xd1}},
		{"pminsb %xmm1, %xmm2", []byte{0x66, 0x0f, 0x38, 0x38, 0xd1}},
		{"pminsd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x38, 0x39, 0xd1}},
		{"pminuw %xmm1, %xmm2", []byte{0x66, 0x0f, 0x38, 0x3a, 0xd1}},
		{"pminud %xmm1, %xmm2", []byte{0x66, 0x0f, 0x38, 0x3b, 0xd1}},
		{"pmaxsb %xmm1, %xmm2", []byte{0x66, 0x0f, 0x38, 0x3c, 0xd1}},
		{"pmaxsd %xmm1, %xmm2", []byte{0x66, 0x0f, 0x38, 0x3d, 0xd1}},
		{"pmaxuw %xmm1, %xmm2", []byte{0x66, 0x0f, 0x38, 0x3e, 0xd1}},
		{"pmaxud %xmm8, %xmm9", []byte{0x66, 0x45, 0x0f, 0x38, 0x3f, 0xc8}},
		{"pshufd $27, %xmm9, %xmm10", []byte{0x66, 0x45, 0x0f, 0x70, 0xd1, 0x1b}},
		{"shufps $228, %xmm1, %xmm2", []byte{0x0f, 0xc6, 0xd1, 0xe4}},
		{"shufpd $1, %xmm3, %xmm4", []byte{0x66, 0x0f, 0xc6, 0xe3, 0x01}},
		{"pextrb $1, %xmm2, %eax", []byte{0x66, 0x0f, 0x3a, 0x14, 0xd0, 0x01}},
		{"pextrw $2, %xmm3, %ecx", []byte{0x66, 0x0f, 0xc5, 0xcb, 0x02}},
		{"pextrd $3, %xmm4, %edx", []byte{0x66, 0x0f, 0x3a, 0x16, 0xe2, 0x03}},
		{"pextrq $1, %xmm5, %rax", []byte{0x66, 0x48, 0x0f, 0x3a, 0x16, 0xe8, 0x01}},
		{"pextrb $1, %xmm2, (%rdi)", []byte{0x66, 0x0f, 0x3a, 0x14, 0x17, 0x01}},
		{"pextrw $1, %xmm2, 2(%rsi)", []byte{0x66, 0x0f, 0x3a, 0x15, 0x56, 0x02, 0x01}},
		{"pextrd $0, %xmm2, (%rdx)", []byte{0x66, 0x0f, 0x3a, 0x16, 0x12, 0x00}},
		{"pextrq $1, %xmm2, 8(%rcx)", []byte{0x66, 0x48, 0x0f, 0x3a, 0x16, 0x51, 0x08, 0x01}},
		{"pinsrb $1, %eax, %xmm2", []byte{0x66, 0x0f, 0x3a, 0x20, 0xd0, 0x01}},
		{"pinsrw $2, %ecx, %xmm3", []byte{0x66, 0x0f, 0xc4, 0xd9, 0x02}},
		{"pinsrd $3, %edx, %xmm4", []byte{0x66, 0x0f, 0x3a, 0x22, 0xe2, 0x03}},
		{"pinsrq $1, %rax, %xmm5", []byte{0x66, 0x48, 0x0f, 0x3a, 0x22, 0xe8, 0x01}},
		{"pinsrw $1, (%rdi), %xmm2", []byte{0x66, 0x0f, 0xc4, 0x17, 0x01}},
		{"pinsrq $0, (%rsi), %xmm3", []byte{0x66, 0x48, 0x0f, 0x3a, 0x22, 0x1e, 0x00}},
		{"crc32b %cl, %eax", []byte{0xf2, 0x0f, 0x38, 0xf0, 0xc1}},
		{"crc32b %sil, %r9d", []byte{0xf2, 0x44, 0x0f, 0x38, 0xf0, 0xce}},
		{"crc32w %cx, %eax", []byte{0x66, 0xf2, 0x0f, 0x38, 0xf1, 0xc1}},
		{"crc32l %ecx, %eax", []byte{0xf2, 0x0f, 0x38, 0xf1, 0xc1}},
		{"crc32q %rcx, %rax", []byte{0xf2, 0x48, 0x0f, 0x38, 0xf1, 0xc1}},
		{"crc32b %cl, %rax", []byte{0xf2, 0x48, 0x0f, 0x38, 0xf0, 0xc1}},
		{"crc32b (%rdi), %eax", []byte{0xf2, 0x0f, 0x38, 0xf0, 0x07}},
		{"crc32q 8(%rsi), %r10", []byte{0xf2, 0x4c, 0x0f, 0x38, 0xf1, 0x56, 0x08}},
		{"cvtsi2sdl %ecx, %xmm0", []byte{0xf2, 0x0f, 0x2a, 0xc1}},
		{"cvtsi2sdq %rcx, %xmm1", []byte{0xf2, 0x48, 0x0f, 0x2a, 0xc9}},
		{"cvtsi2sdq (%rdi), %xmm2", []byte{0xf2, 0x48, 0x0f, 0x2a, 0x17}},
		{"cvtsi2sdl -8(%rbp), %xmm0", []byte{0xf2, 0x0f, 0x2a, 0x45, 0xf8}},
		{"movmskps %xmm1, %eax", []byte{0x0f, 0x50, 0xc1}},
		{"movmskpd %xmm2, %r9d", []byte{0x66, 0x44, 0x0f, 0x50, 0xca}},
		{"pmovmskb %xmm3, %ecx", []byte{0x66, 0x0f, 0xd7, 0xcb}},
	})
	checkRefusedX86(t, bin, runner, []string{
		"crc32w %cx, %rax",
		"crc32q %rcx, %eax",
		"pextrb $1, %xmm2, %rax",
		"movmskps %xmm1, %xmm2",
	})
}

// TestSelfHostX86AlignPadsText pins .p2align/.balign/.align NOP padding in
// .text byte-for-byte against GNU as (binutils' alt_patt multi-byte NOPs,
// an 11-byte fill repeated then one shorter fill), including the max-skip
// third argument. The self-host assembler used to IGNORE .align/.p2align
// in .text, which was only safe while nothing depended on code alignment.
func TestSelfHostX86AlignPadsText(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)
	src := ".text\n_start:\n" +
		"    ret\n" +
		"    .p2align 4\n" + // 15 bytes: the 11-byte fill + the 4-byte one
		"    ret\n" +
		"    .balign 8\n" + // 7 bytes
		"    ret\n" +
		"    .align 4\n" + // byte-count semantics: 3 bytes
		"    ret\n" +
		"    .p2align 3,,2\n" + // pad would be 3 > max-skip 2: skipped
		"    ret\n" +
		"    .p2align 5\n" + // 2 bytes: 66 90
		"    ret\n"
	want := []byte{
		0xc3,
		0x66, 0x66, 0x2e, 0x0f, 0x1f, 0x84, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x0f, 0x1f, 0x40, 0x00,
		0xc3,
		0x0f, 0x1f, 0x80, 0x00, 0x00, 0x00, 0x00,
		0xc3,
		0x0f, 0x1f, 0x00,
		0xc3,
		0xc3,
		0x66, 0x90,
		0xc3,
	}
	got := assembleSelfHostX86(t, bin, runner, src)
	if len(got) != len(want) {
		t.Fatalf(".text length = %d, GNU as = %d\nself-host: % x\nGNU as:    % x", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d: self-host %02x, GNU as %02x\nself-host: % x\nGNU as:    % x", i, got[i], want[i], got, want)
		}
	}
}

// TestSelfHostX86IndirectRip: call/jmp through a rip-relative memory slot
// (`*sym(%rip)`, FF /2 and /4 with a resolved disp32). The expected
// displacements follow the self-host layout: 12 bytes of .text pad to 16,
// where the .rodata quad lands, so the two disp32s are 10 and 4.
func TestSelfHostX86IndirectRip(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)
	src := ".text\n_start:\n" +
		"    call *tbl(%rip)\n" +
		"    jmp *tbl(%rip)\n" +
		".section .rodata\ntbl:\n    .quad 0\n"
	want := []byte{
		0xff, 0x15, 0x0a, 0x00, 0x00, 0x00,
		0xff, 0x25, 0x04, 0x00, 0x00, 0x00,
	}
	got := assembleSelfHostX86(t, bin, runner, src)
	if len(got) != len(want) {
		t.Fatalf(".text length = %d, want %d (% x)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d: got %02x, want %02x (% x)", i, got[i], want[i], got)
		}
	}
}

// TestSelfHostX86ConditionSpellingsGas pins the three condition families
// against the native assembler, spelling by spelling.
//
// Both assemblers dispatch these by matching a prefix and looking the rest up
// in a shared 28-entry table, so there is no per-mnemonic literal for the
// coverage test's source scan to compare — and a family that is invisible to
// that scan is a family where the two can drift silently. That is not
// hypothetical: the self-host hand-listed 13 jCC and 14 setCC spellings while
// the native assembler took all 28, and the coverage test excluded the whole
// family from its reverse direction, so eleven jCC and twelve setCC spellings
// were reachable natively and were RECORDED AS UNKNOWN by the self-host.
//
// `want` comes from the native assembler rather than from a hand-derived byte
// sequence, which for these is the difference between checking the encoding
// and checking that a typo round-trips: the condition code is four bits
// inside the opcode, so a wrong one is a valid instruction that tests the
// wrong flag.
func TestSelfHostX86ConditionSpellingsGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)

	native := func(intel string) []byte {
		t.Helper()
		text, _, err := x86_64.AssembleProgram(intel+"\n", elf.TextVAddr)
		if err != nil {
			t.Fatalf("the native assembler rejects %q, so it cannot be the oracle for it: %v", intel, err)
		}
		return text
	}

	var rows []pinnedX86
	for _, cond := range x86ConditionSpellings {
		// The jCC row is a BACKWARD branch to the label immediately above
		// it. The two assemblers relax in opposite directions — native
		// shrinks from rel32, the self-host grows from rel8 — so a jump
		// whose width either could argue about would be pinning the
		// relaxers rather than the condition table. A zero-distance
		// backward branch is the one shape both must settle on rel8, which
		// leaves the condition nibble as the only thing that can differ.
		// The label is per-row: the rows are assembled as one program, so a
		// shared name would make each jump's displacement depend on where
		// its row landed instead of being -2 everywhere.
		lbl := "lc_" + cond
		rows = append(rows,
			pinnedX86{lbl + ":\nj" + cond + " " + lbl, native(lbl + ":\nj" + cond + " " + lbl)},
			pinnedX86{"set" + cond + " %cl", native("set" + cond + " cl")},
			pinnedX86{"set" + cond + " %r10b", native("set" + cond + " r10b")},
			pinnedX86{"cmov" + cond + " %rcx, %rdx", native("cmov" + cond + " rdx, rcx")},
			pinnedX86{"cmov" + cond + " %ecx, %edx", native("cmov" + cond + " edx, ecx")},
		)
	}
	checkPinnedX86Against(t, bin, runner, "internal/native/x86_64", rows)
}
