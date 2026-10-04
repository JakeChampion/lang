package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestPrBytes(t *testing.T) {
	e2eharness.RunPrByteCases(t, fernBin(t, "pr"), crossPrefix(), nil)
}
