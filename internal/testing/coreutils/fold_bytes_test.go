package coreutils

import (
	"bytes"
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestFoldBytesParity(t *testing.T) {
	e2eharness.RunFoldByteCasesAgainst(t, fernBin(t, "fold"), crossPrefix(), crossArgv(referenceBin(t, "fold")), nil)
}

func TestFoldBytesReferenceCommand(t *testing.T) {
	oracle, marker := digestReferenceCommand(t, "fold")
	t.Cleanup(func() {
		used, err := os.ReadFile(marker)
		want := 1 + len(e2eharness.FoldByteCases())
		if err != nil || bytes.Count(used, []byte("oracle\n")) != want {
			t.Errorf("GNU reference command calls = %d, want %d: %v", bytes.Count(used, []byte("oracle\n")), want, err)
		}
	})
	e2eharness.RunFoldByteCasesAgainst(t, fernBin(t, "fold"), crossPrefix(), oracle, nil)
}
