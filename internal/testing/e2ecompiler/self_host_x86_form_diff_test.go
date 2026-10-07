package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tables/x86tbl"
)

// The operand-form differential (#8083), against GNU as.
//
// The table-driven families (the condition families, the SSE halves, the 0F38
// group) are checked row by row from x86tbl. The GPR families have no table:
// the self-host dispatches them through x86_gas_emit's if-chain, and this
// matrix is what reaches every arm of it.
//
// The mnemonic-level coverage test does not reach this. It asks whether `lea`
// and `movzx` are reachable, and they are; what it cannot ask is whether every
// (operand form, width) of them is. All seven defects #8083 found sat in that
// gap, including one that assembled to a memory load where a register move was
// written.
//
// The matrix is a PRODUCT, deliberately. Probing one form per mnemonic finds
// the refusals and misses the miscompile: `movzwq` had an arm, so a
// mnemonic-shaped or single-form probe passes, and only the register source
// exposes it.

// aluFormCases is every ALU mnemonic at every width in every operand form:
// reg-reg, mem-reg, reg-mem, reg-imm, mem-imm. The mnemonic lists here and
// below come from x86tbl, the table the self-host's dispatch is generated
// from, so a spelling added there is probed here without anyone remembering
// to.
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

// shiftFormCases covers the three count shapes each shift takes — the imm-1
// short form, an imm8, and %cl — at every width, register and memory.
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

// extendLeaFormCases is the family #8083 was found in: AT&T names BOTH widths
// in the mnemonic, so every spelling needs its own arm, in both source forms.
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
		// spl/bpl/sil/dil need a bare REX as a byte SOURCE, and the extended
		// byte registers need one too — the widths must not lose that.
		"movzbl %spl, %esi",
		"movzbq %r9b, %r10",
		"movsbl %sil, %edx",
	}
}

// miscFormCases: the remaining GPR families, register and memory where both
// exist — test, the bit-test group, the RMW atomics, bswap, imul, mov.
func miscFormCases() []string {
	var out []string
	for _, w := range []struct{ sfx, a, b string }{{"l", "%ecx", "%edx"}, {"q", "%rcx", "%rdx"}} {
		out = append(out,
			fmt.Sprintf("test%s %s, %s", w.sfx, w.a, w.b),
			fmt.Sprintf("test%s $7, %s", w.sfx, w.b),
			fmt.Sprintf("bswap%s %s", w.sfx, w.a),
			fmt.Sprintf("imul%s %s, %s", w.sfx, w.a, w.b),
			fmt.Sprintf("imul%s $9, %s, %s", w.sfx, w.a, w.b),
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

// vexFormCases is the AVX2 vocabulary the byte and f64 kernels use, each
// form with a low and an extended register so both halves of the prefix are
// checked.
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
		"vzeroupper",
	}
}

// TestSelfHostX86FormsMatchGas is the gate. Every case is assembled by both
// assemblers and byte-compared; a self-host refusal is a failure, not a skip,
// because a refused line is an instruction that would have left the byte
// stream.
func TestSelfHostX86FormsMatchGas(t *testing.T) {
	var cases []string
	cases = append(cases, aluFormCases()...)
	cases = append(cases, shiftFormCases()...)
	cases = append(cases, extendLeaFormCases()...)
	cases = append(cases, miscFormCases()...)
	cases = append(cases, vexFormCases()...)

	// Anti-vacuity: if the builders stop producing cases the loop below is a
	// no-op that reports success.
	if len(cases) < 300 {
		t.Fatalf("the matrix produced only %d cases; it is meant to be a product of mnemonics, forms and widths", len(cases))
	}

	compareFormCases(t, cases)
}

// compareFormCases assembles every AT&T line with GNU as and with the
// self-host assembler and byte-compares the results. A line GNU as rejects is
// a failure of the case rather than of the self-host.
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
