package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSortBytes(t *testing.T) {
	e2eharness.RunSortByteCases(t, fernBin(t, "sort"), crossPrefix(), nil)
}
