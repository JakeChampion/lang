package main

import (
	"os"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tables/arm64tbl"
)

const arm64NativeFern = "../../compiler/arm64_native.fern"

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
