package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestBaseBytes(t *testing.T) {
	for _, utility := range []string{"base64", "base32", "basenc"} {
		t.Run(utility, func(t *testing.T) {
			e2eharness.RunBaseByteCases(t, utility, fernBin(t, utility), crossPrefix(), nil)
		})
	}
}
