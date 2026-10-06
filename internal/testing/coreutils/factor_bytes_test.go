package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestFactorBytes(t *testing.T) {
	e2eharness.RunFactorByteCases(t, fernBin(t, "factor"), crossPrefix(), nil)
}
