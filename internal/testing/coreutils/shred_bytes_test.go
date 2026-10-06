package coreutils

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestShredBytes(t *testing.T) {
	e2eharness.RunShredByteCases(t, fernBin(t, "shred"), crossPrefix(), nil)
}

func TestShredPatternBytes(t *testing.T) {
	src := e2eharness.WriteShredPatternFixture(t)
	compiler := e2eharness.BuildLangBinForInterp(t)
	bin := filepath.Join(t.TempDir(), "patterns")
	if out, err := exec.Command(compiler, "-target", fernTarget(t), "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	argv := append(append([]string{}, crossPrefix()...), bin)
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if out, err := exec.Command(compiler, "-interp", src).CombinedOutput(); err != nil {
		t.Fatalf("interpreter: %v\n%s", err, out)
	}
}
