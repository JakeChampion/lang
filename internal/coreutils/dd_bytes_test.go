package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestDDBytes(t *testing.T) {
	e2eharness.RunDDByteCases(t, fernBin(t, "dd"), crossPrefix(), nil)
}
