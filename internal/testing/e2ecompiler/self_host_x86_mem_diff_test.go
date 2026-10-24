package e2ecompiler

import (
	"fmt"
	"testing"
)

var memBases = []string{
	"%rax",
	"%rbx",
	"%rsp",
	"%rbp",
	"%rsi",
	"%rdi",
	"%r8",
	"%r12",
	"%r13",
	"%r15",
}

var memDisps = []int{0, 1, 127, 128, -1, -128, -129, 4096}

func attMem(base string, disp int, index string, scale int) string {
	d := ""
	if disp != 0 {
		d = fmt.Sprint(disp)
	}
	if index == "" {
		return fmt.Sprintf("%s(%s)", d, base)
	}
	return fmt.Sprintf("%s(%s,%s,%d)", d, base, index, scale)
}

func memFormCases() []string {
	var out []string
	for _, b := range memBases {
		for _, d := range memDisps {
			out = append(out,
				fmt.Sprintf("movq %s, %%rcx", attMem(b, d, "", 0)),
				fmt.Sprintf("movq %%rcx, %s", attMem(b, d, "", 0)),
				fmt.Sprintf("movl %s, %%ecx", attMem(b, d, "", 0)),
			)
		}
	}
	for _, b := range []string{"%rax", "%rsp", "%rbp", "%r12", "%r13"} {
		for _, idx := range []string{"%rax", "%rcx", "%r12", "%r15"} {
			for _, scale := range []int{1, 2, 4, 8} {
				for _, d := range []int{0, 8, -8} {
					out = append(out, fmt.Sprintf("movq %s, %%rdx", attMem(b, d, idx, scale)))
				}
			}
		}
	}
	return out
}

func TestSelfHostX86MemFormsMatchGas(t *testing.T) {
	cases := memFormCases()
	if len(cases) < 200 {
		t.Fatalf("the matrix produced only %d cases; it is meant to be a product of bases, displacements and index forms", len(cases))
	}
	compareFormCases(t, cases)
}
