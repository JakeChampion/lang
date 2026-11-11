package coreutils

import (
	"bytes"
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestFmtBytes(t *testing.T) {
	e2eharness.RunFmtByteCasesAgainst(t, fernBin(t, "fmt"), crossPrefix(), crossArgv(referenceBin(t, "fmt")), nil)
}

func TestFmtBytesReferenceCommand(t *testing.T) {
	oracle, marker := digestReferenceCommand(t, "fmt")
	t.Cleanup(func() {
		used, err := os.ReadFile(marker)
		want := 1 + 2*len(e2eharness.FmtByteCases())
		if err != nil || bytes.Count(used, []byte("oracle\n")) != want {
			t.Errorf("GNU reference command calls = %d, want %d: %v", bytes.Count(used, []byte("oracle\n")), want, err)
		}
	})
	e2eharness.RunFmtByteCasesAgainst(t, fernBin(t, "fmt"), crossPrefix(), oracle, nil)
}
