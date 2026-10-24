// CI-DARK: FERN_SELFHOST_INTERP — This flag selects the interpreter for developer comparisons.
package e2eharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

func InterpDriverMode() bool {
	return envflag.On("FERN_SELFHOST_INTERP")
}

func writeInterpDriverShim(t testing.TB, dir, fernName, out string) string {
	t.Helper()
	fern := BuildLangBinForInterp(t)
	path := filepath.Join(dir, out)
	script := "#!/bin/sh\n" +
		"if [ -n \"$FERN_DUMP_PROGRAMS\" ]; then\n" +
		"  f=$(mktemp \"$FERN_DUMP_PROGRAMS/" + strings.TrimSuffix(filepath.Base(fernName), ".fern") + ".XXXXXX\")\n" +
		"  cat > \"$f.fern\"; rm -f \"$f\"; exit 0\n" +
		"fi\n" +
		"exec " + fern + " -interp " + filepath.Join(dir, fernName) + " -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write interp driver shim: %v", err)
	}
	return path
}
