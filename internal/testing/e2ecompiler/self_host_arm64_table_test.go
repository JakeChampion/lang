package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tables/arm64tbl"
)

// TestSelfHostArm64TableRowsMatchGas is the vocabulary gate for the self-host
// arm64 assembler, read from arm64tbl (#7903).
//
// arm64tbl.Scalar lists every mnemonic the self-host dispatches by name, with
// a representative instruction per row; the self-host's predicates and
// arm64_gas_known are generated from the same rows (cmd/arm64tblgen's
// staleness test holds the committed output to it). So the SET of mnemonics
// cannot drift. What can still go wrong is a row the self-host's dispatch does
// not reach — the movn shape of #6060, a mnemonic in the allow-list with no
// arm to encode it, which drops the instruction silently — and that is what
// assembling every row through the self-host and GNU as and comparing the
// words catches.
func TestSelfHostArm64TableRowsMatchGas(t *testing.T) {
	var cases, layout []string
	for _, fam := range arm64tbl.Scalar {
		for _, o := range fam.Ops {
			if o.Layout {
				layout = append(layout, fam.ProbeFor(o))
			} else {
				cases = append(cases, fam.ProbeFor(o))
			}
		}
	}
	compareArm64Cases(t, cases)

	// A layout row's word depends on where the image places the sections;
	// acceptance is what it pins.
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	for _, probe := range layout {
		if refused := refusalsFor(t, bin, runner, ".text\n_start:\n"+probe+"\n"); len(refused) > 0 {
			t.Errorf("%-32s the self-host assembler REFUSES it (%s)", probe, strings.Join(refused, ", "))
		}
	}
}

// TestSelfHostArm64VecTableRowsMatchGas is the same gate for the Advanced
// SIMD classes in arm64tbl.VecTables: every mnemonic of every class, in an
// operand shape every row of the class accepts.
func TestSelfHostArm64VecTableRowsMatchGas(t *testing.T) {
	forms := map[string]string{
		"arm64_v3int_entry":      "%s v0.16b, v1.16b, v2.16b",
		"arm64_vlogical_entry":   "%s v0.16b, v1.16b, v2.16b",
		"arm64_vcmpzero_entry":   "%s v0.16b, v1.16b, #0",
		"arm64_v2misc_entry":     "%s v0.16b, v1.16b",
		"arm64_vfp3_entry":       "%s v0.4s, v1.4s, v2.4s",
		"arm64_vfp2_entry":       "%s v0.4s, v1.4s",
		"arm64_vfpcmpzero_entry": "%s v0.4s, v1.4s, #0.0",
		"arm64_vshift_entry":     "%s v0.4s, v1.4s, #3",
		"arm64_vpermute_opc":     "%s v0.16b, v1.16b, v2.16b",
		"arm64_across_entry":     "%s b0, v1.16b",
		"arm64_pairlong_entry":   "%s v0.8h, v1.16b",
		"arm64_vpolylong_entry":  "%s v0.8h, v1.8b, v2.8b",
	}
	var cases []string
	for _, tbl := range arm64tbl.VecTables {
		form, ok := forms[tbl.FernFn]
		if !ok {
			t.Fatalf("no probe form for %s", tbl.FernFn)
		}
		for _, o := range tbl.Ops {
			probe := strings.Replace(form, "%s", o.Mnemonic, 1)
			if tbl.FernFn == "arm64_across_entry" && o.Bool() {
				probe = o.Mnemonic + " h0, v1.16b" // widening: one class up
			}
			if tbl.FernFn == "arm64_vpolylong_entry" && o.Bool() {
				probe = o.Mnemonic + " v0.8h, v1.16b, v2.16b" // the `2` is the Q bit
			}
			cases = append(cases, probe)
		}
	}
	compareArm64Cases(t, cases)
}
