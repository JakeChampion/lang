package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tables/x86tbl"
)

func aluFormCases() []string {
	var out []string
	for _, m := range x86tbl.ALU.Spellings() {
		for _, w := range []struct{ sfx, dst, src string }{
			{"b", "%dl", "%cl"}, {"w", "%dx", "%cx"}, {"l", "%edx", "%ecx"}, {"q", "%rdx", "%rcx"},
		} {
			out = append(out,
				fmt.Sprintf("%s%s %s, %s", m, w.sfx, w.src, w.dst),
				fmt.Sprintf("%s%s (%%rbx), %s", m, w.sfx, w.dst),
				fmt.Sprintf("%s%s %s, (%%rbx)", m, w.sfx, w.dst),
				fmt.Sprintf("%s%s $7, %s", m, w.sfx, w.dst),
				fmt.Sprintf("%s%s $7, (%%rbx)", m, w.sfx),
			)
		}
	}
	return out
}

func shiftFormCases() []string {
	var out []string
	for _, m := range x86tbl.Shift.Spellings() {
		for _, w := range []struct{ sfx, reg string }{{"b", "%dl"}, {"w", "%dx"}, {"l", "%edx"}, {"q", "%rdx"}} {
			out = append(out,
				fmt.Sprintf("%s%s $1, %s", m, w.sfx, w.reg),
				fmt.Sprintf("%s%s $3, %s", m, w.sfx, w.reg),
				fmt.Sprintf("%s%s %%cl, %s", m, w.sfx, w.reg),
				fmt.Sprintf("%s%s $3, (%%rbx)", m, w.sfx),
			)
		}
	}
	return out
}

func extendLeaFormCases() []string {
	return []string{
		"leaw (%rbx), %cx",
		"leal (%rbx), %edx",
		"leaq (%rbx), %rdx",
		"leal (%rbx,%rax,4), %edx",
		"leaq (%rbx,%rax,4), %rdx",
		"leaq -32(%rbp), %rdi",
		"leaq -31(%rax,%rdx), %r9",
		"leaq 31(%r9), %rdx",
		"leaq (%r9,%r11), %rdx",
		"movzbw %cl, %dx",
		"movzbw (%rbx), %dx",
		"movzbl %cl, %edx",
		"movzbl (%rbx), %edx",
		"movzbq %cl, %rdx",
		"movzbq (%rbx), %rdx",
		"movzwl %cx, %edx",
		"movzwl (%rbx), %edx",
		"movzwq %cx, %rdx",
		"movzwq (%rbx), %rdx",
		"movsbw %cl, %dx",
		"movsbl %cl, %edx",
		"movsbl (%rbx), %edx",
		"movsbq %cl, %rdx",
		"movswl %cx, %edx",
		"movswl (%rbx), %edx",
		"movswq %cx, %rdx",
		"movslq %ecx, %rdx",
		"movslq (%rbx), %rdx",
		"movzbl %spl, %esi",
		"movzbq %r9b, %r10",
		"movsbl %sil, %edx",
	}
}

func miscFormCases() []string {
	var out []string
	for _, w := range []struct{ sfx, a, b string }{{"l", "%ecx", "%edx"}, {"q", "%rcx", "%rdx"}} {
		out = append(out,
			fmt.Sprintf("test%s %s, %s", w.sfx, w.a, w.b),
			fmt.Sprintf("test%s $7, %s", w.sfx, w.b),
			fmt.Sprintf("bswap%s %s", w.sfx, w.a),
			fmt.Sprintf("imul%s %s, %s", w.sfx, w.a, w.b),
			fmt.Sprintf("imul%s $9, %s, %s", w.sfx, w.a, w.b),
			fmt.Sprintf("imul%s $-3, %s", w.sfx, w.a),
			fmt.Sprintf("imul%s $972, %s", w.sfx, w.b),
			fmt.Sprintf("mov%s %s, %s", w.sfx, w.a, w.b),
			fmt.Sprintf("mov%s $9, %s", w.sfx, w.b),
			fmt.Sprintf("mov%s (%%rbx), %s", w.sfx, w.b),
			fmt.Sprintf("mov%s %s, (%%rbx)", w.sfx, w.b),
		)
		for _, m := range x86tbl.BitTest.Spellings() {
			out = append(out,
				fmt.Sprintf("%s%s %s, %s", m, w.sfx, w.a, w.b),
				fmt.Sprintf("%s%s $3, %s", m, w.sfx, w.b),
			)
		}
		for _, m := range []string{"xchg", "xadd", "cmpxchg"} {
			out = append(out, fmt.Sprintf("%s%s %s, %s", m, w.sfx, w.a, w.b))
		}
	}
	for _, m := range append(x86tbl.Unary.Spellings(), x86tbl.IncDec.Spellings()...) {
		for _, w := range []struct{ sfx, reg string }{{"b", "%cl"}, {"l", "%ecx"}, {"q", "%rcx"}} {
			out = append(out,
				fmt.Sprintf("%s%s %s", m, w.sfx, w.reg),
				fmt.Sprintf("%s%s (%%rbx)", m, w.sfx),
			)
		}
	}
	return out
}

func vexFormCases() []string {
	return []string{
		"vmovdqu (%rax,%rdx), %ymm0",
		"vmovdqu (%r8,%r9), %ymm3",
		"vmovdqu (%rdi), %ymm9",
		"vmovdqu %ymm1, %ymm0",
		"vpbroadcastb %xmm1, %ymm1",
		"vpbroadcastb %xmm9, %ymm10",
		"vpcmpeqb %ymm1, %ymm0, %ymm0",
		"vpcmpeqb %ymm9, %ymm10, %ymm11",
		"vpcmpeqb (%rax,%rdx), %ymm1, %ymm0",
		"vpcmpeqb (%r9), %ymm1, %ymm0",
		"vpmovmskb %ymm0, %eax",
		"vpmovmskb %ymm0, %r9d",
		"vpmovmskb %ymm10, %r11d",
		"vmovupd 8(%rsi,%rdx,8), %ymm0",
		"vmovupd (%r8,%r9), %ymm11",
		"vmovupd %ymm0, 8(%rax,%rdx,8)",
		"vmovupd %ymm12, (%r10)",
		"vbroadcastsd %xmm1, %ymm1",
		"vbroadcastsd %xmm9, %ymm10",
		"vmulpd %ymm1, %ymm0, %ymm0",
		"vmulpd %ymm9, %ymm10, %ymm11",
		"vmulpd 8(%rsi,%rdx,8), %ymm1, %ymm0",
		"vmulpd (%r8,%r9,8), %ymm12, %ymm13",
		"vmovdqu (%r9), %xmm0",
		"vmovdqu (%r10), %xmm11",
		"vmovdqu %xmm3, 16(%rdi)",
		"vpmovmskb %xmm1, %edx",
		"vpmovmskb %xmm9, %r11d",
		"vbroadcasti128 (%r11), %ymm2",
		"vbroadcasti128 80(%r11), %ymm7",
		"vbroadcasti128 16(%rax), %ymm12",
		"vpand %ymm5, %ymm0, %ymm1",
		"vpand %xmm13, %xmm8, %xmm8",
		"vpor %ymm9, %ymm1, %ymm1",
		"vpor %xmm1, %xmm10, %xmm12",
		"vpxor %ymm6, %ymm0, %ymm8",
		"vpxor (%rax), %xmm1, %xmm2",
		"vpshufb %ymm1, %ymm2, %ymm9",
		"vpshufb %xmm8, %xmm3, %xmm1",
		"vpshufb %ymm12, %ymm11, %ymm10",
		"vpcmpeqb %xmm8, %xmm1, %xmm1",
		"vpaddusb %ymm5, %ymm0, %ymm3",
		"vpaddusb %xmm12, %xmm9, %xmm1",
		"vpsubb %ymm4, %ymm0, %ymm0",
		"vpsubb (%rdx), %xmm11, %xmm13",
		"vpsadbw %ymm3, %ymm2, %ymm2",
		"vpsadbw %xmm12, %xmm9, %xmm1",
		"vpsadbw (%r9,%r11,4), %ymm8, %ymm13",
		"vpminub %ymm3, %ymm1, %ymm8",
		"vpminub %xmm3, %xmm1, %xmm8",
		"vpminub %ymm12, %ymm9, %ymm10",
		"vpaddb %ymm3, %ymm1, %ymm8",
		"vpaddb %xmm13, %xmm9, %xmm2",
		"vpaddb (%rdx), %xmm11, %xmm13",
		"vpmaxub %ymm12, %ymm9, %ymm10",
		"vpmaxub %xmm3, %xmm1, %xmm8",
		"vpaddsb %xmm4, %xmm12, %xmm1",
		"vpaddsb %ymm9, %ymm2, %ymm11",
		"vpsubsb %xmm14, %xmm0, %xmm5",
		"vpsubsb %ymm1, %ymm10, %ymm3",
		"vpavgb %xmm2, %xmm8, %xmm9",
		"vpavgb %ymm13, %ymm4, %ymm0",
		"vpminsb %xmm7, %xmm1, %xmm12",
		"vpminsb %ymm10, %ymm11, %ymm2",
		"vpmaxsb %xmm15, %xmm6, %xmm3",
		"vpmaxsb %ymm0, %ymm9, %ymm14",
		"vpcmpgtb %xmm5, %xmm13, %xmm1",
		"vpcmpgtb %ymm8, %ymm2, %ymm10",
		"vpsrlw %xmm3, %xmm1, %xmm8",
		"vpsrlw (%rdx), %xmm11, %xmm13",
		"vpaddw %xmm3, %xmm1, %xmm8",
		"vpaddw %ymm12, %ymm9, %ymm10",
		"vpaddd %ymm12, %ymm9, %ymm10",
		"vpaddd (%rdx), %xmm11, %xmm13",
		"vpsubw (%rdx), %xmm11, %xmm13",
		"vpsubw %xmm3, %xmm1, %xmm8",
		"vpsubd %xmm3, %xmm1, %xmm8",
		"vpsubd %ymm12, %ymm9, %ymm10",
		"vpaddsw %ymm12, %ymm9, %ymm10",
		"vpaddsw (%rdx), %xmm11, %xmm13",
		"vpaddusw (%rdx), %xmm11, %xmm13",
		"vpaddusw %xmm3, %xmm1, %xmm8",
		"vpsubsw %xmm3, %xmm1, %xmm8",
		"vpsubsw %ymm12, %ymm9, %ymm10",
		"vpsubusw %ymm12, %ymm9, %ymm10",
		"vpsubusw (%rdx), %xmm11, %xmm13",
		"vpminsw (%rdx), %xmm11, %xmm13",
		"vpminsw %xmm3, %xmm1, %xmm8",
		"vpmaxsw %xmm3, %xmm1, %xmm8",
		"vpmaxsw %ymm12, %ymm9, %ymm10",
		"vpminuw %ymm12, %ymm9, %ymm10",
		"vpminuw (%rdx), %xmm11, %xmm13",
		"vpmaxuw (%rdx), %xmm11, %xmm13",
		"vpmaxuw %xmm3, %xmm1, %xmm8",
		"vpminsd %xmm3, %xmm1, %xmm8",
		"vpminsd %ymm12, %ymm9, %ymm10",
		"vpmaxsd %ymm12, %ymm9, %ymm10",
		"vpmaxsd (%rdx), %xmm11, %xmm13",
		"vpminud (%rdx), %xmm11, %xmm13",
		"vpminud %xmm3, %xmm1, %xmm8",
		"vpmaxud %xmm3, %xmm1, %xmm8",
		"vpmaxud %ymm12, %ymm9, %ymm10",
		"vpavgw %ymm12, %ymm9, %ymm10",
		"vpavgw (%rdx), %xmm11, %xmm13",
		"vpcmpeqw (%rdx), %xmm11, %xmm13",
		"vpcmpeqw %xmm3, %xmm1, %xmm8",
		"vpcmpeqd %xmm3, %xmm1, %xmm8",
		"vpcmpeqd %ymm12, %ymm9, %ymm10",
		"vpcmpgtw %ymm12, %ymm9, %ymm10",
		"vpcmpgtw (%rdx), %xmm11, %xmm13",
		"vpcmpgtd (%rdx), %xmm11, %xmm13",
		"vpcmpgtd %xmm3, %xmm1, %xmm8",
		"vpmulld %xmm3, %xmm1, %xmm8",
		"vpmulld %ymm12, %ymm9, %ymm10",
		"vpsllw %xmm12, %xmm9, %xmm10",
		"vpsllw (%rdx), %xmm11, %xmm13",
		"vpslld (%rdx), %xmm11, %xmm13",
		"vpslld %xmm3, %xmm1, %xmm8",
		"vpsrld %xmm3, %xmm1, %xmm8",
		"vpsrld %xmm12, %xmm9, %xmm10",
		"vpsraw %xmm12, %xmm9, %xmm10",
		"vpsraw (%rdx), %xmm11, %xmm13",
		"vpsrad (%rdx), %xmm11, %xmm13",
		"vpsrad %xmm3, %xmm1, %xmm8",
		"vpbroadcastb 352(%r11), %ymm2",
		"vpbroadcastb 355(%r11), %ymm5",
		"vpbroadcastb (%rax), %ymm12",
		"vpbroadcastd %xmm13, %ymm13",
		"vpbroadcastd %xmm7, %ymm9",
		"vpmulhuw %ymm14, %ymm1, %ymm1",
		"vpmulhuw %xmm9, %xmm10, %xmm11",
		"vpmullw %ymm7, %ymm2, %ymm2",
		"vpmullw %ymm12, %ymm9, %ymm8",
		"vpackuswb %ymm1, %ymm0, %ymm0",
		"vpackuswb %xmm9, %xmm10, %xmm11",
		"vpackuswb %ymm12, %ymm9, %ymm8",
		"vpbroadcastb %xmm8, %xmm8",
		"vpbroadcastb %xmm12, %xmm3",
		"vpbroadcastd %xmm1, %xmm9",
		"vpmaddubsw %xmm13, %xmm1, %xmm1",
		"vpmaddubsw %ymm9, %ymm10, %ymm11",
		"vpmaddwd %xmm14, %xmm1, %xmm1",
		"vpmaddwd %ymm2, %ymm12, %ymm8",
		"vpsllq $44, %xmm1, %xmm2",
		"vpsrlq $24, %xmm2, %xmm2",
		"vpsrlq $32, %xmm9, %xmm10",
		"vpsllq $3, %ymm11, %ymm12",
		"vpsrlw $4, %ymm1, %ymm2",
		"vpslld $5, %ymm0, %ymm5",
		"vpsrld $27, %ymm9, %ymm14",
		"vpslld $30, %ymm12, %ymm3",
		"vpsrld $2, %xmm1, %xmm2",
		"vperm2i128 $32, %ymm4, %ymm0, %ymm8",
		"vperm2i128 $49, %ymm12, %ymm9, %ymm3",
		"vperm2i128 $32, 32(%rsp), %ymm10, %ymm1",
		"vpaddd 64(%rsp), %ymm1, %ymm1",
		"vpaddd %ymm9, %ymm10, %ymm11",
		"vpxor 480(%rsp), %ymm5, %ymm5",
		"vpand %ymm2, %ymm8, %ymm8",
		"vpor %ymm13, %ymm6, %ymm6",
		"vpshufb %ymm15, %ymm3, %ymm3",
		"vpunpckldq %ymm1, %ymm0, %ymm8",
		"vpunpckhdq %ymm11, %ymm10, %ymm9",
		"vpunpcklqdq %ymm10, %ymm8, %ymm0",
		"vpunpckhqdq %ymm3, %ymm2, %ymm12",
		"vmovdqu %ymm5, 96(%rsp)",
		"vmovdqu 448(%rsi), %ymm7",
		"vzeroupper",
		"bswapq %rax",
		"bswapq %r11",
		"notl %edx",
		"xorl $65535, %edx",
		"vpsubusb %ymm12, %ymm2, %ymm5",
		"vpsubusb %xmm13, %xmm3, %xmm6",
		"vpsubusb %ymm9, %ymm10, %ymm11",
		"vpsrlw $4, %ymm0, %ymm0",
		"vpsrlw $4, %xmm9, %xmm0",
		"vpsrlw $3, %ymm2, %ymm10",
		"vzeroupper",
		"vpunpckldq %xmm1, %xmm2, %xmm3",
		"vpunpckldq %xmm9, %xmm10, %xmm11",
		"vpunpckldq %xmm3, %xmm2, %xmm3",
		"vpunpckldq 16(%r8,%rdx,8), %xmm12, %xmm13",
		"vpunpckldq -24(%rbp), %ymm1, %ymm2",
		"vpunpckldq 8(%rip), %ymm12, %ymm13",
		"vpunpckhdq %xmm1, %xmm2, %xmm3",
		"vpunpckhdq %xmm9, %xmm10, %xmm11",
		"vpunpckhdq %xmm3, %xmm2, %xmm3",
		"vpunpckhdq 16(%r8,%rdx,8), %xmm12, %xmm13",
		"vpunpckhdq -24(%rbp), %ymm1, %ymm2",
		"vpunpckhdq 8(%rip), %ymm12, %ymm13",
		"vpunpcklqdq %xmm1, %xmm2, %xmm3",
		"vpunpcklqdq %xmm9, %xmm10, %xmm11",
		"vpunpcklqdq %xmm3, %xmm2, %xmm3",
		"vpunpcklqdq 16(%r8,%rdx,8), %xmm12, %xmm13",
		"vpunpcklqdq -24(%rbp), %ymm1, %ymm2",
		"vpunpcklqdq 8(%rip), %ymm12, %ymm13",
		"vpunpckhqdq %xmm1, %xmm2, %xmm3",
		"vpunpckhqdq %xmm9, %xmm10, %xmm11",
		"vpunpckhqdq %xmm3, %xmm2, %xmm3",
		"vpunpckhqdq 16(%r8,%rdx,8), %xmm12, %xmm13",
		"vpunpckhqdq -24(%rbp), %ymm1, %ymm2",
		"vpunpckhqdq 8(%rip), %ymm12, %ymm13",
		"vaddpd %xmm9, %xmm10, %xmm11",
		"vsubpd -24(%rbp), %xmm12, %xmm13",
		"vmulpd 16(%r8,%rdx,8), %xmm1, %xmm0",
		"vdivpd %ymm3, %ymm4, %ymm5",
		"vandpd (%rax), %xmm1, %xmm0",
		"vorpd %xmm12, %xmm9, %xmm1",
		"vxorpd %xmm2, %xmm1, %xmm0",
		"vandnpd %xmm9, %xmm10, %xmm11",
		"vminpd -40(%rbp), %xmm12, %xmm13",
		"vmaxpd %ymm3, %ymm4, %ymm5",
		"vaddps %xmm9, %xmm10, %xmm11",
		"vsubps -24(%rbp), %xmm12, %xmm13",
		"vmulps 16(%r8,%rdx,8), %xmm1, %xmm0",
		"vdivps %ymm3, %ymm4, %ymm5",
		"vandps (%rax), %xmm1, %xmm0",
		"vorps %xmm12, %xmm9, %xmm1",
		"vxorps %xmm2, %xmm1, %xmm0",
		"vandnps %xmm9, %xmm10, %xmm11",
		"vminps -40(%rbp), %xmm12, %xmm13",
		"vmaxps %ymm3, %ymm4, %ymm5",
		"vfmadd213pd %xmm2, %xmm1, %xmm0",
		"vfmadd213pd %xmm9, %xmm10, %xmm11",
		"vfmadd213pd (%rax), %xmm1, %xmm0",
		"vfmadd213pd 16(%r8,%rdx,8), %xmm12, %xmm13",
		"vfmadd213pd %ymm3, %ymm4, %ymm5",
		"vfmadd213ps %xmm2, %xmm1, %xmm0",
		"vfmadd213ps %xmm9, %xmm10, %xmm11",
		"vfmadd213ps (%r9), %xmm1, %xmm0",
		"vfmadd213ps %ymm12, %ymm11, %ymm10",
	}
}

// chachaFormCases are the forms of the ChaCha20 kernel, __fern_chacha20_xor_into.
func chachaFormCases() []string {
	return []string{
		"vpshufb 0(%r10), %ymm12, %ymm12",
		"vpshufb 32(%r10), %ymm15, %ymm15",
		"vpaddd 64(%r10), %ymm0, %ymm0",
		"vpaddd 384(%rsp), %ymm13, %ymm13",
		"vpxor 480(%rsi), %ymm4, %ymm4",
		"vpxor %ymm0, %ymm0, %ymm0",
		"vmovq %rax, %xmm0",
		"vpbroadcastd %xmm0, %ymm0",
		"vpslld $12, %ymm4, %ymm10",
		"vpsrld $20, %ymm7, %ymm7",
		"vpsrld $25, %ymm5, %ymm5",
		"vmovdqu %ymm0, 1504(%rsp)",
		"vmovdqu 832(%rsp), %ymm8",
		"andq $-32, %rsp",
		"subq $1536, %rsp",
		"movq (%rsi,%rcx), %rax",
		"xorq 1024(%rsp,%rcx), %rax",
		"movq %rax, (%rsi,%rcx)",
		"movzbl 1024(%rsp,%rcx), %r9d",
		"movb %al, (%rsi,%rcx)",
		"leaq 8(%rcx), %rax",
		"movslq %r8d, %r8",
		"subl $1, %eax",
	}
}

func TestSelfHostX86FormsMatchGas(t *testing.T) {
	var cases []string
	cases = append(cases, aluFormCases()...)
	cases = append(cases, shiftFormCases()...)
	cases = append(cases, extendLeaFormCases()...)
	cases = append(cases, miscFormCases()...)
	cases = append(cases, vexFormCases()...)
	cases = append(cases, chachaFormCases()...)

	if len(cases) < 300 {
		t.Fatalf("the matrix produced only %d cases; it is meant to be a product of mnemonics, forms and widths", len(cases))
	}

	compareFormCases(t, cases)
}

func compareFormCases(t *testing.T, cases []string) {
	t.Helper()
	gas := gnuX86Oracle(t)
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)

	for i, want := range gas.assemble(t, cases) {
		c := cases[i]
		if want.rejected != "" {
			t.Errorf("%q: GNU as rejects it, so it cannot be the oracle for it: %s", c, want.rejected)
			continue
		}
		out := runX86BenchDriver(t, bin, runner, ".text\n_start:\n    "+c+"\n", "-bytes")
		if refused := asmRefusals(out); len(refused) > 0 {
			t.Errorf("%-34q the self-host assembler REFUSES it; GNU as emits % x", c, want.bytes)
			continue
		}
		var got []byte
		for _, ln := range strings.Split(out, "\n") {
			var idx, val int
			if _, e := fmt.Sscanf(ln, "byte %d %d", &idx, &val); e == nil {
				got = append(got, byte(val))
			}
		}
		if string(got) != string(want.bytes) {
			t.Errorf("%-34q self-host % x, GNU as % x", c, got, want.bytes)
		}
	}
}
