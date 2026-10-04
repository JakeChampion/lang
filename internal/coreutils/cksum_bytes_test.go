package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestCksumBytes(t *testing.T) {
	e2eharness.RunCksumByteCases(t, fernBin(t, "cksum"), crossPrefix(), nil)
}
