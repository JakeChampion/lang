package e2ecompiler

import (
	"fmt"
	"testing"
)

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
	for _, v := range []string{"-2147483648", "-1", "0", "2147483647"} {
		out = append(out, fmt.Sprintf("movq $%s, %%rax", v))
	}
	for _, v := range []string{"2147483648", "4294967295", "4294967296"} {
		out = append(out, fmt.Sprintf("movabsq $%s, %%rax", v))
	}
	return out
}

func pushPopFormCases() []string {
	regs := []string{"rax", "rcx", "rdx", "rbx", "rsp", "rbp", "rsi", "rdi",
		"r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15"}
	var out []string
	for _, r := range regs {
		out = append(out, fmt.Sprintf("pushq %%%s", r), fmt.Sprintf("popq %%%s", r))
	}
	for _, v := range []string{"-128", "-1", "0", "127", "128", "-129", "1000"} {
		out = append(out, fmt.Sprintf("pushq $%s", v))
	}
	for _, base := range []string{"rax", "rsp", "rbp", "r12", "r13"} {
		out = append(out, fmt.Sprintf("pushq (%%%s)", base), fmt.Sprintf("popq (%%%s)", base))
	}
	return out
}

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
