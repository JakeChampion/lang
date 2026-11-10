package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestDigestCheckBytes(t *testing.T) {
	for _, utility := range e2eharness.DigestCheckUtilities {
		t.Run(utility, func(t *testing.T) {
			e2eharness.RunDigestCheckByteCasesAgainst(t, utility, fernBin(t, utility), crossPrefix(), crossArgv(referenceBin(t, utility)), nil)
		})
	}
}

func TestDigestCheckBytesReferenceCommand(t *testing.T) {
	oracle, marker := digestReferenceCommand(t, "b2sum")
	e2eharness.RunDigestCheckByteCasesAgainst(t, "b2sum", fernBin(t, "b2sum"), crossPrefix(), oracle, nil)
	requireDigestReferenceCommand(t, marker)
}

func digestReferenceCommand(t *testing.T, utility string) ([]string, string) {
	t.Helper()
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "oracle-used")
	oracle := []string{shell, "-c", `marker=$1; shift; printf 'oracle\n' >> "$marker"; exec "$@"`, "digest-oracle", marker}
	oracle = append(oracle, crossArgv(referenceBin(t, utility))...)
	return oracle, marker
}

func requireDigestReferenceCommand(t *testing.T, marker string) {
	t.Helper()
	used, err := os.ReadFile(marker)
	if err != nil || len(used) == 0 {
		t.Fatalf("GNU reference command prefix did not execute: %v", err)
	}
}
