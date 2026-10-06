package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestCksumBytes(t *testing.T) {
	e2eharness.RunCksumByteCases(t, fernBin(t, "cksum"), crossPrefix(), nil)
}
