package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tables/arm64tbl"
)

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

	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	for _, probe := range layout {
		if refused := refusalsFor(t, bin, runner, ".text\n_start:\n"+probe+"\n"); len(refused) > 0 {
			t.Errorf("%-32s the self-host assembler REFUSES it (%s)", probe, strings.Join(refused, ", "))
		}
	}
}

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
				probe = o.Mnemonic + " h0, v1.16b"
			}
			if tbl.FernFn == "arm64_vpolylong_entry" && o.Bool() {
				probe = o.Mnemonic + " v0.8h, v1.16b, v2.16b"
			}
			cases = append(cases, probe)
		}
	}
	compareArm64Cases(t, cases)
}
