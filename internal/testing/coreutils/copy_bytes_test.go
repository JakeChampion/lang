package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestCopyBytes(t *testing.T) {
	for _, utility := range []string{"cp", "install", "mv"} {
		t.Run(utility, func(t *testing.T) {
			e2eharness.RunCopyByteCases(t, utility, fernBin(t, utility), crossPrefix(), nil)
		})
	}
}
