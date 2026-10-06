package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestNumfmtBytes(t *testing.T) {
	e2eharness.RunNumfmtByteCases(t, fernBin(t, "numfmt"), crossPrefix(), nil)
}
