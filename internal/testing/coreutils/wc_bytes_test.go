package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestWcBytesParity(t *testing.T) {
	e2eharness.RunWcByteCases(t, fernBin(t, "wc"), crossPrefix(), nil)
}
