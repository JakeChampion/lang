package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tables/x86tbl"
)

// TestSelfHostX86TableRowsMatchGas is the vocabulary gate for the self-host
// x86-64 assembler, read from x86tbl, the table its predicates and lookups are
// generated from (#7903): the by-name families, the ModRM-extension groups,
// the no-operand vocabulary, the two-byte SSE table and the condition
// families. Every row carries a representative AT&T instruction, which goes
// through both the self-host assembler and GNU as, and the bytes must agree.
//
// cmd/x86tblgen's staleness test holds the generated predicates to the table,
// so the SET of spellings cannot drift. What this catches is a row the
// self-host's dispatch does not reach, or reaches with the wrong data — the
// shape every vocabulary defect so far has had (#8000, #8020, #8071, #8083).
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
	// The no-operand vocabulary: every AT&T spelling gas takes.
	for _, f := range x86tbl.FixedOps {
		cases = append(cases, f.Spellings...)
	}
	// Both operand shapes: the rows are the `xmm <- xmm/mem` forms, and a row
	// that only worked register-to-register would be half a form.
	for _, o := range x86tbl.SSEOps {
		cases = append(cases, o.Mnemonic+" %xmm1, %xmm0", o.Mnemonic+" (%rax), %xmm1")
	}
	for _, cond := range x86tbl.CondSpellings() {
		cases = append(cases, "set"+cond+" %cl", "cmov"+cond+" %rcx, %rax")
	}
	// The lock set reaches the prefix path.
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
