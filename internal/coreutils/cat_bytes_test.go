package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestCatBytesParity(t *testing.T) {
	e2eharness.RunCatByteCases(t, fernBin(t, "cat"), crossPrefix(), nil)
}
