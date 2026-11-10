package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestCatBytesParity(t *testing.T) {
	e2eharness.RunCatByteCasesAgainst(t, fernBin(t, "cat"), crossPrefix(), crossArgv(referenceBin(t, "cat")), nil)
}

func TestCatBytesReferenceCommand(t *testing.T) {
	oracle, marker := digestReferenceCommand(t, "cat")
	e2eharness.RunCatByteCasesAgainst(t, fernBin(t, "cat"), crossPrefix(), oracle, nil)
	requireDigestReferenceCommand(t, marker)
}
