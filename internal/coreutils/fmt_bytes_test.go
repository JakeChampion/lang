package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestFmtBytes(t *testing.T) {
	e2eharness.RunFmtByteCases(t, fernBin(t, "fmt"), nil, nil)
}
