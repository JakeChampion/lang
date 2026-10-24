package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/stdlib"
)

func TestStdlibModulesImportStandalone(t *testing.T) {
	modules, err := stdlib.Modules()
	if err != nil {
		t.Fatalf("walk stdlib FS: %v", err)
	}
	if len(modules) == 0 {
		t.Fatal("no stdlib modules discovered")
	}

	for _, mod := range modules {
		if strings.HasPrefix(filepath.Base(mod), "_test") {
			continue
		}
		t.Run(mod, func(t *testing.T) {
			src := "import \"" + mod + "\";\n" +
				"function main(): i32 { return 0; }\n"
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			prog, _, err := modload.Load(p)
			if err != nil {
				t.Fatalf("import %q does not load standalone: %v", mod, err)
			}
			if err := constfold.Fold(prog, nil); err != nil {
				t.Fatalf("import %q: constfold: %v", mod, err)
			}
			if _, err := checker.Check(prog); err != nil {
				t.Fatalf("import %q does not type-check standalone (missing a dependency import?):\n%v", mod, err)
			}
		})
	}
}
