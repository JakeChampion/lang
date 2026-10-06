package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSplitBytes(t *testing.T) {
	e2eharness.RunSplitByteCases(t, fernBin(t, "split"), crossPrefix(), nil)
}
