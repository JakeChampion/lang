package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestBaseBytes(t *testing.T) {
	for _, utility := range []string{"base64", "base32", "basenc"} {
		t.Run(utility, func(t *testing.T) {
			e2eharness.RunBaseByteCasesAgainst(t, utility, fernBin(t, utility), crossPrefix(), crossArgv(referenceBin(t, utility)), nil)
		})
	}
}

func TestBaseBytesReferenceCommand(t *testing.T) {
	oracle := append([]string{"sh", "-c", `exec "$@"`, "gnu-reference"}, crossArgv(referenceBin(t, "basenc"))...)
	e2eharness.RunBaseByteCasesAgainst(t, "basenc", fernBin(t, "basenc"), crossPrefix(), oracle, nil)
}
