package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestNlBytes(t *testing.T) {
	e2eharness.RunNlByteCases(t, fernBin(t, "nl"), nil, nil)
}
