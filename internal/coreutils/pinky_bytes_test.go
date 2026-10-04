package coreutils

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestPinkyBytes(t *testing.T) {
	src := e2eharness.WritePinkyByteFixture(t)
	compiler := e2eharness.BuildLangBinForInterp(t)
	bin := filepath.Join(t.TempDir(), "pinky-files")
	if out, err := exec.Command(compiler, "-target", fernTarget(t), "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	e2eharness.RunPinkyByteCases(t, bin, crossPrefix(), nil)
}
