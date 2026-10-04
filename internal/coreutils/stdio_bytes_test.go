package coreutils

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestStdioBytes(t *testing.T) {
	src := e2eharness.WriteStdioByteFixture(t)
	compiler := e2eharness.BuildLangBinForInterp(t)
	bin := filepath.Join(t.TempDir(), "stdio-bytes")
	if out, err := exec.Command(compiler, "-target", fernTarget(t), "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	e2eharness.RunStdioByteCases(t, bin, crossPrefix(), nil)
}
