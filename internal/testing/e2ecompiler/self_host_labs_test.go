package e2ecompiler

import (
	"path/filepath"
	"testing"
)

func TestSelfHostLabsCore(t *testing.T) {
	entry, err := filepath.Abs("../../../examples/showcase/tests/labs-core.fern")
	if err != nil {
		t.Fatal(err)
	}
	testFractalTargets(t, entry)
}
