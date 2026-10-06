package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestDateBytes(t *testing.T) {
	e2eharness.RunDateByteCases(t, fernBin(t, "date"), crossPrefix(), nil)
}
