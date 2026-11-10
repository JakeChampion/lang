package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestTacBytes(t *testing.T) {
	e2eharness.RunTacByteCasesAgainst(t, fernBin(t, "tac"), crossPrefix(), crossArgv(referenceBin(t, "tac")), nil)
}

func TestTacBytesReferenceCommand(t *testing.T) {
	oracle, marker := digestReferenceCommand(t, "tac")
	e2eharness.RunTacByteCasesAgainst(t, fernBin(t, "tac"), crossPrefix(), oracle, nil)
	requireDigestReferenceCommand(t, marker)
}
