package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestPtxBytes(t *testing.T) {
	e2eharness.RunPtxByteCases(t, fernBin(t, "ptx"), nil, nil)
}
