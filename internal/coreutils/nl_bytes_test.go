package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestNlBytes(t *testing.T) {
	e2eharness.RunNlByteCases(t, fernBin(t, "nl"), nil, nil)
}
