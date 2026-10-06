package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestCsplitBytes(t *testing.T) {
	e2eharness.RunCsplitByteCases(t, fernBin(t, "csplit"), nil, nil)
}
