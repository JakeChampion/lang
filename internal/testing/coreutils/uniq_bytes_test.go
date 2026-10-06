package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestUniqBytes(t *testing.T) {
	e2eharness.RunUniqByteCases(t, fernBin(t, "uniq"), crossPrefix(), nil)
}
