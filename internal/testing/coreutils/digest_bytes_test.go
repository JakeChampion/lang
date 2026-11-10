package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestDigestBytes(t *testing.T) {
	e2eharness.RunDigestByteCasesAgainst(t, fernBin(t, "cksum"), crossPrefix(), crossArgv(referenceBin(t, "cksum")), nil)
}

func TestDigestBytesReferenceCommand(t *testing.T) {
	oracle, marker := digestReferenceCommand(t, "cksum")
	e2eharness.RunDigestByteCasesAgainst(t, fernBin(t, "cksum"), crossPrefix(), oracle, nil)
	requireDigestReferenceCommand(t, marker)
}
