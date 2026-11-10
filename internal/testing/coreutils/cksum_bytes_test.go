package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestCksumBytes(t *testing.T) {
	e2eharness.RunCksumByteCasesAgainst(t, fernBin(t, "cksum"), crossPrefix(), crossArgv(referenceBin(t, "cksum")), nil)
}

func TestCksumBytesReferenceCommand(t *testing.T) {
	oracle, marker := digestReferenceCommand(t, "cksum")
	e2eharness.RunCksumByteCasesAgainst(t, fernBin(t, "cksum"), crossPrefix(), oracle, nil)
	requireDigestReferenceCommand(t, marker)
}
