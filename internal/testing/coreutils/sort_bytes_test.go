package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSortBytes(t *testing.T) {
	e2eharness.RunSortByteCasesAgainst(t, fernBin(t, "sort"), crossPrefix(), crossArgv(referenceBin(t, "sort")), nil)
}

func TestSortBytesReferenceCommand(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "oracle-used")
	oracle := []string{shell, "-c", `marker=$1; shift; printf 'oracle\n' >> "$marker"; exec "$@"`, "sort-oracle", marker}
	oracle = append(oracle, crossArgv(referenceBin(t, "sort"))...)
	e2eharness.RunSortByteCasesAgainst(t, fernBin(t, "sort"), crossPrefix(), oracle, nil)
	used, err := os.ReadFile(marker)
	if err != nil || len(used) == 0 {
		t.Fatalf("GNU reference command prefix did not execute: %v", err)
	}
}
