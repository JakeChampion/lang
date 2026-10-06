package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestDUBytes(t *testing.T) {
	e2eharness.RunDUByteCases(t, fernBin(t, "du"), crossPrefix(), fernTarget(t), nil)
}
