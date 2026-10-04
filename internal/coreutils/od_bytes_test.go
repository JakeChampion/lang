package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestOdBytes(t *testing.T) {
	e2eharness.RunOdByteCases(t, fernBin(t, "od"), nil, nil)
}
