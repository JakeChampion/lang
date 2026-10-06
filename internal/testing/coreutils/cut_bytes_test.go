package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestCutBytesParity(t *testing.T) {
	e2eharness.RunCutByteCases(t, fernBin(t, "cut"), crossPrefix(), nil)
}
