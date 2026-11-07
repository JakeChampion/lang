package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/printer"
)

// The stdin drivers need a complete program with no unresolved imports.
func assemblerSource(t *testing.T, name string) string {
	t.Helper()
	prog, _, err := modload.Load(langSrcAbs(t, "compiler/"+name))
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return printer.Format(prog)
}

// CLI fixtures retain imports in the copied compiler project.
func writeAssemblerProgram(t *testing.T, dir, name, program string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "test_"+name)
	if err := os.WriteFile(path, append(source, []byte("\n"+program)...), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
