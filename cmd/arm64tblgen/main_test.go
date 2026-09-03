package main

import (
	"os"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
	"github.com/jakechampion/lang/internal/native/arm64tbl"
)

const arm64NativeFern = "../../examples/self_host/arm64_native.fern"

// TestGeneratedFernIsUpToDate is the gate that makes the shared table real:
// a hand edit to either the table or a generated block fails here rather
// than surfacing as a mnemonic one assembler accepts and the other drops.
func TestGeneratedFernIsUpToDate(t *testing.T) {
	src, err := os.ReadFile(arm64NativeFern)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Rewrite(string(src))
	if err != nil {
		t.Fatal(err)
	}
	if out != string(src) {
		t.Errorf("%s is out of date; regenerate with:\n\tgo run ./cmd/arm64tblgen %s", arm64NativeFern, arm64NativeFern)
	}
}

// TestMarkersArePresent keeps the test above from passing vacuously: Rewrite
// leaves a file with no markers untouched.
func TestMarkersArePresent(t *testing.T) {
	src, err := os.ReadFile(arm64NativeFern)
	if err != nil {
		t.Fatal(err)
	}
	var wanted []string
	for _, tbl := range arm64tbl.VecTables {
		begin, end := markers(tbl)
		wanted = append(wanted, begin, end)
	}
	wanted = append(wanted, scalarBegin, scalarEnd)
	for _, m := range wanted {
		if !strings.Contains(string(src), m) {
			t.Errorf("%s carries no %q — Rewrite would leave it alone and the staleness check would pass on any content", arm64NativeFern, m)
		}
	}
}

// TestGoAssemblerAcceptsEveryScalarRow is the same loop for the by-name
// vocabulary: every row's probe assembles through the Go assembler, so a
// family added to the table without a dispatch arm, or a probe that names
// a shape the encoder refuses, fails here. The self-host side of the same
// probes is internal/e2eselfhost's TestSelfHostArm64TableRowsMatchNative.
func TestGoAssemblerAcceptsEveryScalarRow(t *testing.T) {
	seen := map[string]bool{}
	for _, fam := range arm64tbl.Scalar {
		if len(fam.Ops) == 0 {
			t.Errorf("family %q has no rows", fam.Name)
		}
		for _, o := range fam.Ops {
			if seen[o.Mnemonic] {
				t.Errorf("%q is listed twice", o.Mnemonic)
			}
			seen[o.Mnemonic] = true
			probe := fam.ProbeFor(o)
			if !strings.HasPrefix(probe, o.Mnemonic+" ") && probe != o.Mnemonic {
				t.Errorf("%s: probe %q does not start with the mnemonic", o.Mnemonic, probe)
			}
			if _, _, err := arm64.AssembleProgram(".text\n"+probe+"\n", 0x400000); err != nil {
				t.Errorf("%s (%s): %v", o.Mnemonic, fam.Name, err)
			}
		}
	}
}

// TestGoAssemblerAcceptsEveryRow closes the loop on the other side: every
// mnemonic in every class assembles through the Go assembler in that class's
// operand shape, so a row in the table is a row both assemblers reach.
func TestGoAssemblerAcceptsEveryRow(t *testing.T) {
	// One probe per class, at an arrangement every row of it accepts.
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
	}
	for _, tbl := range arm64tbl.VecTables {
		form, ok := forms[tbl.FernFn]
		if !ok {
			t.Fatalf("no probe form for %s", tbl.FernFn)
		}
		seen := map[string]bool{}
		for _, o := range tbl.Ops {
			if seen[o.Mnemonic] {
				t.Errorf("%s lists %q twice", tbl.FernFn, o.Mnemonic)
			}
			seen[o.Mnemonic] = true
			probe := strings.Replace(form, "%s", o.Mnemonic, 1)
			if tbl.FernFn == "arm64_across_entry" && o.Bool() {
				probe = o.Mnemonic + " h0, v1.16b" // widening: one class up
			}
			if _, _, err := arm64.AssembleProgram(".text\n"+probe+"\n", 0x400000); err != nil {
				t.Errorf("%q: %v", probe, err)
			}
		}
	}
}
