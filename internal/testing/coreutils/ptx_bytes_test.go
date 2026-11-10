package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestPtxBytes(t *testing.T) {
	e2eharness.RunPtxByteCasesAgainst(t, fernBin(t, "ptx"), crossPrefix(), crossArgv(referenceBin(t, "ptx")), nil)
}

func TestPtxBytesReferenceCommand(t *testing.T) {
	oracle, marker := digestReferenceCommand(t, "ptx")
	e2eharness.RunPtxByteCasesAgainst(t, fernBin(t, "ptx"), crossPrefix(), oracle, nil)
	requireDigestReferenceCommand(t, marker)
}
