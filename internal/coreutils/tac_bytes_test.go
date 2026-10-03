package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestTacBytes(t *testing.T) {
	e2eharness.RunTacByteCases(t, fernBin(t, "tac"), nil, nil)
}
