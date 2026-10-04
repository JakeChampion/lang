package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSumBytesParity(t *testing.T) {
	e2eharness.RunSumByteCases(t, fernBin(t, "sum"), crossPrefix(), nil)
}
