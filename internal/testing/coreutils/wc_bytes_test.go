package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestWcBytesParity(t *testing.T) {
	e2eharness.RunWcByteCasesAgainst(t, fernBin(t, "wc"), crossPrefix(), crossArgv(referenceBin(t, "wc")), nil)
}

func TestWcBytesReferenceCommand(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "oracle-used")
	// The prefix records execution before it starts the real GNU oracle.
	oracle := []string{shell, "-c", `marker=$1; shift; printf 'oracle\n' >> "$marker"; exec "$@"`, "wc-oracle", marker}
	oracle = append(oracle, crossArgv(referenceBin(t, "wc"))...)
	e2eharness.RunWcByteCasesAgainst(t, fernBin(t, "wc"), crossPrefix(), oracle, nil)
	used, err := os.ReadFile(marker)
	if err != nil || len(used) == 0 {
		t.Fatalf("GNU reference command prefix did not execute: %v", err)
	}
}
