package main

import (
	"os"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/arm64tbl"
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

// TestScalarRowsAreWellFormed guards the by-name vocabulary's own shape: a
// mnemonic listed twice would dispatch to one of its two rows, and a probe
// that does not start with its mnemonic probes some other row. The self-host
// side of the same probes is internal/e2eselfhost's
// TestSelfHostArm64TableRowsMatchGas.
func TestScalarRowsAreWellFormed(t *testing.T) {
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
		}
	}
}

// TestVecRowsAreDistinct: a mnemonic listed twice in a class would dispatch
// to one of its two rows. The self-host side of the same rows is
// internal/e2eselfhost's TestSelfHostArm64VecTableRowsMatchGas.
func TestVecRowsAreDistinct(t *testing.T) {
	for _, tbl := range arm64tbl.VecTables {
		seen := map[string]bool{}
		for _, o := range tbl.Ops {
			if seen[o.Mnemonic] {
				t.Errorf("%s lists %q twice", tbl.FernFn, o.Mnemonic)
			}
			seen[o.Mnemonic] = true
		}
	}
}
