package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestFactorBytes(t *testing.T) {
	e2eharness.RunFactorByteCases(t, fernBin(t, "factor"), crossPrefix(), nil)
}
