package e2eselfhost

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/x86tbl"
)

// TestSelfHostX86TableRowsMatchNative is the vocabulary gate between the two
// x86-64 assemblers, read from the tables both are built from (#7903): the
// by-name families, the ModRM-extension groups, the no-operand vocabulary,
// the two-byte SSE table and the condition families. Every row carries a
// representative instruction in each dialect; the Intel line goes through
// internal/native/x86_64 and the AT&T line through the self-host assembler,
// and the bytes must agree.
//
// The Go assembler dispatches by looking a mnemonic up in these tables, and
// the self-host's predicates and lookups are generated from them
// (cmd/x86tblgen's staleness test holds the committed output to the table),
// so the SET of spellings cannot drift any more. What this still catches is
// a row the self-host's dispatch does not reach, or reaches with the wrong
// data — the shape every vocabulary defect so far has had (#8000, #8020,
// #8071, #8083).
//
// The gate this replaced read the mnemonic set out of x86_gas_emit with a
// regular expression and probed a hand-kept list of 300 entries. It could
// see only the literal comparisons, so a family dispatched by pattern had to
// be excluded by hand, and #8071 opened inside such an exclusion.
func TestSelfHostX86TableRowsMatchNative(t *testing.T) {
	var cases []formCase
	for _, fam := range x86tbl.Named {
		for _, o := range fam.Ops {
			cases = append(cases, formCase{o.ATTProbe, o.Probe})
		}
	}
	for _, g := range x86tbl.Groups {
		for _, m := range g.Spellings() {
			cases = append(cases, formCase{
				strings.Replace(g.ATTProbe, "%s", m, 1),
				strings.Replace(g.Probe, "%s", m, 1),
			})
		}
	}
	// The no-operand vocabulary: every spelling gas takes in a dialect goes
	// through that dialect's assembler. Where a row has spellings in both,
	// each AT&T spelling is paired with the row's first Intel one.
	for _, f := range x86tbl.FixedOps {
		intel := f.IntelSpellings()[0]
		for _, s := range f.ATTSpellings() {
			cases = append(cases, formCase{s, intel})
		}
	}
	for _, o := range x86tbl.SSEOps {
		cases = append(cases, formCase{o.Mnemonic + " %xmm1, %xmm0", o.Mnemonic + " xmm0, xmm1"})
	}
	for _, cond := range x86tbl.CondSpellings() {
		cases = append(cases,
			formCase{"set" + cond + " %cl", "set" + cond + " cl"},
			formCase{"cmov" + cond + " %rcx, %rax", "cmov" + cond + " rax, rcx"},
		)
	}
	if len(cases) < 300 {
		t.Fatalf("the tables produced only %d cases", len(cases))
	}
	compareFormCases(t, cases)
}
