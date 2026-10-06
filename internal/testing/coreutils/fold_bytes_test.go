package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestFoldBytesParity(t *testing.T) {
	e2eharness.RunFoldByteCases(t, fernBin(t, "fold"), crossPrefix(), nil)
}
