package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestJoinBytesParity(t *testing.T) {
	e2eharness.RunJoinByteCases(t, fernBin(t, "join"), crossPrefix(), nil)
}
