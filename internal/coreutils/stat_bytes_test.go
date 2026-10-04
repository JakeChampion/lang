package coreutils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestStatBytesParity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, e2eharness.StatByteFilename), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	var cases []invocation
	for _, tc := range e2eharness.StatByteCases() {
		cases = append(cases, invocation{name: tc.Name, dir: dir,
			args: []string{"--printf", tc.Format, e2eharness.StatByteFilename}})
	}
	requireParity(t, "stat", cases)
	e2eharness.RunStatByteCases(t, fernBin(t, "stat"), crossPrefix(), nil)
}
