package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestPrBytes(t *testing.T) {
	e2eharness.RunPrByteCasesAgainst(t, fernBin(t, "pr"), crossPrefix(), crossArgv(referenceBin(t, "pr")), nil)
}

func TestPrBytesReferenceCommand(t *testing.T) {
	oracle, marker := digestReferenceCommand(t, "pr")
	e2eharness.RunPrByteCasesAgainst(t, fernBin(t, "pr"), crossPrefix(), oracle, nil)
	requireDigestReferenceCommand(t, marker)
}
