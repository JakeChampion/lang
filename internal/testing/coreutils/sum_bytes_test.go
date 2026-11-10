package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSumBytesParity(t *testing.T) {
	e2eharness.RunSumByteCasesAgainst(t, fernBin(t, "sum"), crossPrefix(), crossArgv(referenceBin(t, "sum")), nil)
}

func TestSumBytesReferenceCommand(t *testing.T) {
	oracle, marker := digestReferenceCommand(t, "sum")
	e2eharness.RunSumByteCasesAgainst(t, fernBin(t, "sum"), crossPrefix(), oracle, nil)
	requireDigestReferenceCommand(t, marker)
}
