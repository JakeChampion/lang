package e2ecompiler

import (
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
