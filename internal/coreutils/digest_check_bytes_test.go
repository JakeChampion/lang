package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestDigestCheckBytes(t *testing.T) {
	for _, utility := range e2eharness.DigestCheckUtilities {
		t.Run(utility, func(t *testing.T) {
			e2eharness.RunDigestCheckByteCases(t, utility, fernBin(t, utility), crossPrefix(), nil)
		})
	}
}
