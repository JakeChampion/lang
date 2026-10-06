package e2ecompiler

import (
	"fmt"
	"testing"
)

// #7903 phase 4: the last families dispatched by hand.
//
// The tables now cover the ALU/shift/unary/inc-dec digits, the conditions,
// the SSE halves and the no-operand vocabulary. What is left with no table
// AND no product probe is mov/movabs, push/pop, and the indirect branch
// forms — and the existing coverage of them is thin in a specific way:
// `mov` was probed at l and q only in four forms, push/pop only ever with
// %rax, and call/jmp only ever as `call $symbol`.
//
// Two selection rules live in here, which is why it is worth a product
// rather than a handful of cases:
//
//   - `movq $imm, %reg` picks C7 /0 with a SIGN-extended imm32 when the
//     value fits signed-32, and the 10-byte movabs otherwise. Testing the
//     UNSIGNED range instead would encode 0x80000000 as C7 /0, which
//     sign-extends to 0xFFFFFFFF80000000 — a different number, silently.
//   - `pushq $imm` picks the 2-byte 6A ib when the value fits signed-8 and
//     the 5-byte 68 id otherwise.
//
// The self-host was checked to get these right before this test was written;
// it exists so it cannot stop.

// movFormCases: every width in every operand form, plus the immediate
// selection boundary in both directions.
func movFormCases() []string {
	var out []string
	for _, w := range []struct{ sfx, dst, src string }{
		{"b", "%dl", "%cl"}, {"w", "%dx", "%cx"}, {"l", "%edx", "%ecx"}, {"q", "%rdx", "%rcx"},
	} {
		out = append(out,
			fmt.Sprintf("mov%s %s, %s", w.sfx, w.src, w.dst),
			fmt.Sprintf("mov%s (%%rbx), %s", w.sfx, w.dst),
			fmt.Sprintf("mov%s %s, (%%rbx)", w.sfx, w.dst),
			fmt.Sprintf("mov%s $7, %s", w.sfx, w.dst),
			fmt.Sprintf("mov%s $7, (%%rbx)", w.sfx),
		)
	}
	// The imm32/imm64 boundary. -0x80000000 and 0x7fffffff are the last
	// values on each side that still fit the sign-extended field; 0x80000000
	// is the first that does not and must promote to movabs.
	for _, v := range []string{"-2147483648", "-1", "0", "2147483647"} {
		out = append(out, fmt.Sprintf("movq $%s, %%rax", v))
	}
	for _, v := range []string{"2147483648", "4294967295", "4294967296"} {
		out = append(out, fmt.Sprintf("movabsq $%s, %%rax", v))
	}
	return out
}

// pushPopFormCases: every register, so the REX.B boundary at %r8 is covered
// rather than assumed, plus the immediate selection and the memory forms.
func pushPopFormCases() []string {
	regs := []string{"rax", "rcx", "rdx", "rbx", "rsp", "rbp", "rsi", "rdi",
		"r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15"}
	var out []string
	for _, r := range regs {
		out = append(out, fmt.Sprintf("pushq %%%s", r), fmt.Sprintf("popq %%%s", r))
	}
	// 6A ib below the signed-8 edge, 68 id above it.
	for _, v := range []string{"-128", "-1", "0", "127", "128", "-129", "1000"} {
		out = append(out, fmt.Sprintf("pushq $%s", v))
	}
	for _, base := range []string{"rax", "rsp", "rbp", "r12", "r13"} {
		out = append(out, fmt.Sprintf("pushq (%%%s)", base), fmt.Sprintf("popq (%%%s)", base))
	}
	return out
}

// indirectBranchFormCases: `call *%reg` / `jmp *mem`, the forms nothing in
// the self-host suite probed — every prior case was `call $symbol`.
func indirectBranchFormCases() []string {
	var out []string
	for _, r := range []string{"rax", "rcx", "rsp", "rbp", "r8", "r12", "r13", "r15"} {
		out = append(out,
			fmt.Sprintf("call *%%%s", r),
			fmt.Sprintf("jmp *%%%s", r),
			fmt.Sprintf("call *(%%%s)", r),
			fmt.Sprintf("jmp *(%%%s)", r),
		)
	}
	for _, scale := range []int{1, 2, 4, 8} {
		out = append(out,
			fmt.Sprintf("jmp *(%%rax,%%rcx,%d)", scale),
			fmt.Sprintf("call *(%%rax,%%rcx,%d)", scale),
		)
	}
	return out
}

// TestSelfHostX86MovBranchFormsMatchGas byte-compares the three families
// against GNU as.
func TestSelfHostX86MovBranchFormsMatchGas(t *testing.T) {
	var cases []string
	cases = append(cases, movFormCases()...)
	cases = append(cases, pushPopFormCases()...)
	cases = append(cases, indirectBranchFormCases()...)
	if len(cases) < 100 {
		t.Fatalf("the matrix produced only %d cases; it is meant to be a product of mnemonics, forms and widths", len(cases))
	}
	compareFormCases(t, cases)
}
