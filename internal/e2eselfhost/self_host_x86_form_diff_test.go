package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/x86_64"
	"github.com/jakechampion/lang/internal/native/x86tbl"
)

// The operand-form differential (#8083).
//
// The two x86-64 assemblers have byte-level differentials for everything that
// lives in a TABLE — the condition families, the SSE halves, the 0F38 group.
// The GPR families have no table on either side: Go dispatches through insn's
// switch, the self-host through x86_gas_emit's if-chain, and until this
// nothing compared them at all.
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

// formCase is one instruction in both dialects. The AT&T text goes to the
// self-host assembler, the Intel text to internal/native/x86_64, and the bytes
// must agree.
type formCase struct{ att, intel string }

// aluFormCases is every ALU mnemonic at every width in every operand form:
// reg-reg, mem-reg, reg-mem, reg-imm, mem-imm. The mnemonic lists here and
// below come from x86tbl, the table both assemblers dispatch from, so a
// spelling added there is probed here without anyone remembering to.
func aluFormCases() []formCase {
	var out []formCase
	for _, m := range x86tbl.ALU.Spellings() {
		for _, w := range []struct{ sfx, att, intel, size string }{
			{"b", "%dl", "dl", "byte ptr"},
			{"w", "%dx", "dx", "word ptr"},
			{"l", "%edx", "edx", "dword ptr"},
			{"q", "%rdx", "rdx", "qword ptr"},
		} {
			src := map[string]string{"b": "%cl", "w": "%cx", "l": "%ecx", "q": "%rcx"}[w.sfx]
			isrc := map[string]string{"b": "cl", "w": "cx", "l": "ecx", "q": "rcx"}[w.sfx]
			out = append(out,
				formCase{fmt.Sprintf("%s%s %s, %s", m, w.sfx, src, w.att), fmt.Sprintf("%s %s, %s", m, w.intel, isrc)},
				formCase{fmt.Sprintf("%s%s (%%rbx), %s", m, w.sfx, w.att), fmt.Sprintf("%s %s, %s [rbx]", m, w.intel, w.size)},
				formCase{fmt.Sprintf("%s%s %s, (%%rbx)", m, w.sfx, w.att), fmt.Sprintf("%s %s [rbx], %s", m, w.size, w.intel)},
				formCase{fmt.Sprintf("%s%s $7, %s", m, w.sfx, w.att), fmt.Sprintf("%s %s, 7", m, w.intel)},
				formCase{fmt.Sprintf("%s%s $7, (%%rbx)", m, w.sfx), fmt.Sprintf("%s %s [rbx], 7", m, w.size)},
			)
		}
	}
	return out
}

// shiftFormCases covers the three count shapes each shift takes — the imm-1
// short form, an imm8, and %cl — at every width, register and memory.
func shiftFormCases() []formCase {
	var out []formCase
	for _, m := range x86tbl.Shift.Spellings() {
		for _, w := range []struct{ sfx, att, intel, size string }{
			{"b", "%dl", "dl", "byte ptr"}, {"w", "%dx", "dx", "word ptr"},
			{"l", "%edx", "edx", "dword ptr"}, {"q", "%rdx", "rdx", "qword ptr"},
		} {
			out = append(out,
				formCase{fmt.Sprintf("%s%s $1, %s", m, w.sfx, w.att), fmt.Sprintf("%s %s, 1", m, w.intel)},
				formCase{fmt.Sprintf("%s%s $3, %s", m, w.sfx, w.att), fmt.Sprintf("%s %s, 3", m, w.intel)},
				formCase{fmt.Sprintf("%s%s %%cl, %s", m, w.sfx, w.att), fmt.Sprintf("%s %s, cl", m, w.intel)},
				formCase{fmt.Sprintf("%s%s $3, (%%rbx)", m, w.sfx), fmt.Sprintf("%s %s [rbx], 3", m, w.size)},
			)
		}
	}
	return out
}

// extendLeaFormCases is the family #8083 was found in: AT&T names BOTH widths
// in the mnemonic where Intel reads them off the operands, so every spelling
// needs its own arm, in both source forms.
func extendLeaFormCases() []formCase {
	return []formCase{
		{"leaw (%rbx), %cx", "lea cx, [rbx]"},
		{"leal (%rbx), %edx", "lea edx, [rbx]"},
		{"leaq (%rbx), %rdx", "lea rdx, [rbx]"},
		{"leal (%rbx,%rax,4), %edx", "lea edx, [rbx+rax*4]"},
		{"leaq (%rbx,%rax,4), %rdx", "lea rdx, [rbx+rax*4]"},
		{"leaq -32(%rbp), %rdi", "lea rdi, [rbp-32]"},
		{"movzbw %cl, %dx", "movzx dx, cl"},
		{"movzbw (%rbx), %dx", "movzx dx, byte ptr [rbx]"},
		{"movzbl %cl, %edx", "movzx edx, cl"},
		{"movzbl (%rbx), %edx", "movzx edx, byte ptr [rbx]"},
		{"movzbq %cl, %rdx", "movzx rdx, cl"},
		{"movzbq (%rbx), %rdx", "movzx rdx, byte ptr [rbx]"},
		{"movzwl %cx, %edx", "movzx edx, cx"},
		{"movzwl (%rbx), %edx", "movzx edx, word ptr [rbx]"},
		{"movzwq %cx, %rdx", "movzx rdx, cx"},
		{"movzwq (%rbx), %rdx", "movzx rdx, word ptr [rbx]"},
		{"movsbw %cl, %dx", "movsx dx, cl"},
		{"movsbl %cl, %edx", "movsx edx, cl"},
		{"movsbl (%rbx), %edx", "movsx edx, byte ptr [rbx]"},
		{"movsbq %cl, %rdx", "movsx rdx, cl"},
		{"movswl %cx, %edx", "movsx edx, cx"},
		{"movswl (%rbx), %edx", "movsx edx, word ptr [rbx]"},
		{"movswq %cx, %rdx", "movsx rdx, cx"},
		{"movslq %ecx, %rdx", "movsxd rdx, ecx"},
		{"movslq (%rbx), %rdx", "movsxd rdx, dword ptr [rbx]"},
		// spl/bpl/sil/dil need a bare REX as a byte SOURCE, and the extended
		// byte registers need one too — the widths must not lose that.
		{"movzbl %spl, %esi", "movzx esi, spl"},
		{"movzbq %r9b, %r10", "movzx r10, r9b"},
		{"movsbl %sil, %edx", "movsx edx, sil"},
	}
}

// miscFormCases: the remaining GPR families, register and memory where both
// exist — test, the bit-test group, the RMW atomics, bswap, imul, mov.
func miscFormCases() []formCase {
	var out []formCase
	for _, w := range []struct{ sfx, a, b, ia, ib, size string }{
		{"l", "%ecx", "%edx", "ecx", "edx", "dword ptr"},
		{"q", "%rcx", "%rdx", "rcx", "rdx", "qword ptr"},
	} {
		out = append(out,
			formCase{fmt.Sprintf("test%s %s, %s", w.sfx, w.a, w.b), fmt.Sprintf("test %s, %s", w.ib, w.ia)},
			formCase{fmt.Sprintf("test%s $7, %s", w.sfx, w.b), fmt.Sprintf("test %s, 7", w.ib)},
			formCase{fmt.Sprintf("bswap%s %s", w.sfx, w.a), fmt.Sprintf("bswap %s", w.ia)},
			formCase{fmt.Sprintf("imul%s %s, %s", w.sfx, w.a, w.b), fmt.Sprintf("imul %s, %s", w.ib, w.ia)},
			formCase{fmt.Sprintf("imul%s $9, %s, %s", w.sfx, w.a, w.b), fmt.Sprintf("imul %s, %s, 9", w.ib, w.ia)},
			formCase{fmt.Sprintf("mov%s %s, %s", w.sfx, w.a, w.b), fmt.Sprintf("mov %s, %s", w.ib, w.ia)},
			formCase{fmt.Sprintf("mov%s $9, %s", w.sfx, w.b), fmt.Sprintf("mov %s, 9", w.ib)},
			formCase{fmt.Sprintf("mov%s (%%rbx), %s", w.sfx, w.b), fmt.Sprintf("mov %s, %s [rbx]", w.ib, w.size)},
			formCase{fmt.Sprintf("mov%s %s, (%%rbx)", w.sfx, w.b), fmt.Sprintf("mov %s [rbx], %s", w.size, w.ib)},
		)
		for _, m := range x86tbl.BitTest.Spellings() {
			out = append(out,
				formCase{fmt.Sprintf("%s%s %s, %s", m, w.sfx, w.a, w.b), fmt.Sprintf("%s %s, %s", m, w.ib, w.ia)},
				formCase{fmt.Sprintf("%s%s $3, %s", m, w.sfx, w.b), fmt.Sprintf("%s %s, 3", m, w.ib)},
			)
		}
		for _, m := range []string{"xchg", "xadd", "cmpxchg"} {
			out = append(out, formCase{fmt.Sprintf("%s%s %s, %s", m, w.sfx, w.a, w.b), fmt.Sprintf("%s %s, %s", m, w.ib, w.ia)})
		}
	}
	for _, m := range append(x86tbl.Unary.Spellings(), x86tbl.IncDec.Spellings()...) {
		for _, w := range []struct{ sfx, att, intel, size string }{
			{"b", "%cl", "cl", "byte ptr"}, {"l", "%ecx", "ecx", "dword ptr"}, {"q", "%rcx", "rcx", "qword ptr"},
		} {
			out = append(out,
				formCase{fmt.Sprintf("%s%s %s", m, w.sfx, w.att), fmt.Sprintf("%s %s", m, w.intel)},
				formCase{fmt.Sprintf("%s%s (%%rbx)", m, w.sfx), fmt.Sprintf("%s %s [rbx]", m, w.size)},
			)
		}
	}
	return out
}

// TestSelfHostX86FormsMatchNative is the gate. Every case is assembled by both
// assemblers and byte-compared; a self-host refusal is a failure, not a skip,
// because a refused line is an instruction that would have left the byte
// stream.
func TestSelfHostX86FormsMatchNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)

	var cases []formCase
	cases = append(cases, aluFormCases()...)
	cases = append(cases, shiftFormCases()...)
	cases = append(cases, extendLeaFormCases()...)
	cases = append(cases, miscFormCases()...)

	// Anti-vacuity: if the builders stop producing cases the loop below is a
	// no-op that reports success.
	if len(cases) < 300 {
		t.Fatalf("the matrix produced only %d cases; it is meant to be a product of mnemonics, forms and widths", len(cases))
	}

	for _, c := range cases {
		want, _, err := x86_64.AssembleProgram(c.intel+"\n", 0x400000)
		if err != nil {
			t.Errorf("%q: internal/native/x86_64 rejects it, so it cannot be the oracle for %q: %v", c.intel, c.att, err)
			continue
		}
		out := runX86BenchDriver(t, bin, runner, ".text\n_start:\n    "+c.att+"\n", "-bytes")
		if refused := asmRefusals(out); len(refused) > 0 {
			t.Errorf("%-32q the self-host assembler REFUSES it; native emits % x", c.att, want)
			continue
		}
		var got []byte
		for _, ln := range strings.Split(out, "\n") {
			var idx, val int
			if _, e := fmt.Sscanf(ln, "byte %d %d", &idx, &val); e == nil {
				got = append(got, byte(val))
			}
		}
		if string(got) != string(want) {
			t.Errorf("%-32q self-host % x, internal/native/x86_64 % x", c.att, got, want)
		}
	}
}
