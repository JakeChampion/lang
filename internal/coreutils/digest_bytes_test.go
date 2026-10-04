package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestDigestBytes(t *testing.T) {
	e2eharness.RunDigestByteCases(t, fernBin(t, "cksum"), crossPrefix(), nil)
}
