package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestCutBytesParity(t *testing.T) {
	e2eharness.RunCutByteCases(t, fernBin(t, "cut"), crossPrefix(), nil)
}
