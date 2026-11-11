package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestDDBytes(t *testing.T) {
	e2eharness.RunDDByteCasesAgainst(t, fernBin(t, "dd"), crossPrefix(), crossArgv(referenceBin(t, "dd")), nil)
}

func TestDDBytesReferenceCommand(t *testing.T) {
	oracle := append([]string{"sh", "-c", `exec "$@"`, "gnu-reference"}, crossArgv(referenceBin(t, "dd"))...)
	e2eharness.RunDDByteCasesAgainst(t, fernBin(t, "dd"), crossPrefix(), oracle, nil)
}
