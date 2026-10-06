package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestDigestBytes(t *testing.T) {
	e2eharness.RunDigestByteCases(t, fernBin(t, "cksum"), crossPrefix(), nil)
}
