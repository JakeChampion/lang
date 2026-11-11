package e2ecompiler

import (
	"fmt"
	"testing"
)

func TestSelfHostX86RIPConstantsMatchGas(t *testing.T) {
	var cases []string
	for _, displacement := range []string{"", "0", "8", "+8", "-8", "010", "0x1234", "0b1000", "2147483647", "-2147483648"} {
		for _, form := range []string{
			"vpunpckldq %s(%%rip), %%xmm1, %%xmm0",
			"vshufps $136, %s(%%rip), %%xmm1, %%xmm0",
			"vshufps $221, %s(%%rip), %%ymm12, %%ymm13",
			"vpunpckhdq %s(%%rip), %%ymm12, %%ymm13",
			"vpunpcklqdq %s(%%rip), %%xmm10, %%xmm9",
			"vpunpckhqdq %s(%%rip), %%ymm1, %%ymm0",
			"vmovdqu %s(%%rip), %%ymm12",
			"vpand %s(%%rip), %%ymm1, %%ymm0",
			"pshufb %s(%%rip), %%xmm8",
			"movq %s(%%rip), %%r9",
			"movl %%r9d, %s(%%rip)",
			"movq $7, %s(%%rip)",
			"leaq %s(%%rip), %%rax",
			"cmpq $7, %s(%%rip)",
		} {
			cases = append(cases, fmt.Sprintf(form, displacement))
		}
	}
	compareFormCases(t, cases)
}

func TestSelfHostX86VEXRejectsMixedWidths(t *testing.T) {
	var cases []string
	for _, op := range []string{"vpunpckldq", "vpunpckhdq", "vpunpcklqdq", "vpunpckhqdq", "vpand", "vmulpd", "vfmadd213ps", "vshufps $136,"} {
		for _, operands := range []string{
			"%ymm2, %xmm1, %xmm0", "%xmm2, %ymm1, %ymm0",
			"%xmm2, %ymm1, %xmm0", "%ymm2, %xmm1, %ymm0",
			"%xmm2, %xmm1, %ymm0", "%ymm2, %ymm1, %xmm0",
		} {
			cases = append(cases, op+" "+operands)
		}
	}
	for _, offset := range []string{"2147483648", "-2147483649", "0xffffffff", "+", "0x", "0b2"} {
		cases = append(cases, "vpunpckldq "+offset+"(%rip), %xmm1, %xmm0")
	}
	gas := gnuX86Oracle(t)
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)
	for i, result := range gas.assemble(t, cases) {
		if result.rejected == "" {
			t.Fatalf("GNU as accepts invalid case %q", cases[i])
		}
		if refused := refusalsForX86(t, bin, runner, ".text\n_start:\n "+cases[i]+"\n"); len(refused) == 0 {
			t.Errorf("native assembler accepts %q", cases[i])
		}
	}
}

func TestSelfHostX86GasRIPConstants(t *testing.T) {
	runX86GasWasmSelfTest(t, "rip_constants", `
function main(): i32 {
 let a = x86_gas_assemble(".text\n_start:\n vpunpckldq 8(%rip), %ymm12, %ymm13\n");
 let expected: i32[] = [197, 29, 98, 45, 8, 0, 0, 0];
 if (a.unknown.len() != 0 || a.text.len() != expected.len()) { return 1; }
 for i in 0..expected.len() { if (a.text[i] as i32 != expected[i]) { return 2; } }
 let b = x86_gas_assemble(".text\n_start:\n vpand %ymm2, %xmm1, %xmm0\n");
 if (b.unknown.len() == 0) { return 3; }
 return 0;
}`)
}

func TestSelfHostX86ShuffleFormsMatchGas(t *testing.T) {
	var cases []string
	for _, width := range []string{"xmm", "ymm"} {
		for _, imm := range []int{0, 34, 119, 136, 221, 255} {
			for _, regs := range [][3]int{{0, 1, 2}, {9, 10, 11}, {3, 2, 3}, {1, 2, 2}, {15, 15, 15}} {
				cases = append(cases, fmt.Sprintf("vshufps $%d, %%%s%d, %%%s%d, %%%s%d", imm, width, regs[0], width, regs[1], width, regs[2]))
			}
			for _, mem := range []string{"16(%r8,%rdx,8)", "-24(%rbp)", "(%r12,%r13,4)"} {
				cases = append(cases, fmt.Sprintf("vshufps $%d, %s, %%%s12, %%%s13", imm, mem, width, width))
			}
		}
	}
	cases = append(cases, "vshufps $136, .Lshuffle(%rip), %xmm12, %xmm13\n.Lshuffle:\n.long 0,1,2,3")
	compareFormCases(t, cases)
}
