package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestTrBytesParity(t *testing.T) {
	e2eharness.RunTrByteCases(t, fernBin(t, "tr"), crossPrefix(), nil)
}
