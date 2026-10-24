package e2ecompiler

import (
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tables/x86tbl"
)

func TestSelfHostX86TableRowsMatchGas(t *testing.T) {
	var cases []string
	for _, fam := range x86tbl.Named {
		for _, o := range fam.Ops {
			cases = append(cases, o.ATTProbe)
		}
	}
	for _, g := range x86tbl.Groups {
		for _, m := range g.Spellings() {
			cases = append(cases, strings.Replace(g.ATTProbe, "%s", m, 1))
		}
	}
	for _, f := range x86tbl.FixedOps {
		cases = append(cases, f.Spellings...)
	}
	for _, o := range x86tbl.SSEOps {
		cases = append(cases, o.Mnemonic+" %xmm1, %xmm0")
		if !slices.Contains(x86tbl.SSERegOnly, o.Mnemonic) {
			cases = append(cases, o.Mnemonic+" (%rax), %xmm1")
		}
	}
	for _, cond := range x86tbl.CondSpellings() {
		cases = append(cases, "set"+cond+" %cl", "cmov"+cond+" %rcx, %rax")
	}
	for _, sp := range x86tbl.LockableSpellings() {
		switch sp {
		case "inc", "dec", "not", "neg":
			cases = append(cases, "lock "+sp+"q (%rbx)")
		default:
			cases = append(cases, "lock "+sp+"q %rax, (%rbx)")
		}
	}
	if len(cases) < 300 {
		t.Fatalf("the tables produced only %d cases", len(cases))
	}
	compareFormCases(t, cases)
}

func TestSelfHostX86SSERegOnlyRefusesMemory(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)
	for _, m := range x86tbl.SSERegOnly {
		c := m + " (%rax), %xmm1"
		out := runX86BenchDriver(t, bin, runner, ".text\n_start:\n    "+c+"\n", "-bytes")
		if len(asmRefusals(out)) == 0 {
			t.Errorf("%q: the self-host assembler accepts it\n%s", c, out)
		}
	}
}
