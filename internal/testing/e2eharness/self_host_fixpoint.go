package e2eharness

import (
	"os"
	"path/filepath"
	"testing"
)

func BuildBin(t *testing.T, gcc, dir, name, asm string) string {
	t.Helper()
	asmPath := filepath.Join(dir, name+".s")
	binPath := filepath.Join(dir, name)
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write %s asm: %v", name, err)
	}
	copyExecutable(t, CachedLink(t, gcc, asm), binPath)
	return binPath
}
