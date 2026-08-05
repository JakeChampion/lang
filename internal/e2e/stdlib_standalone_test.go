package e2e

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/stdlib"
)

// TestStdlibModulesImportStandalone is the flip-era regression gate:
// every std/* and core/* module must type-check when imported on its
// own under no-prelude. The auto-prelude used to load the whole tree,
// which masked modules that called a method / free function from
// another module without declaring the `import` (std/time → std/string,
// std/test → std/float, std/fuzz → std/test all had this). A module
// that can't be imported alone is a latent break waiting for the first
// program that reaches for it.
func TestStdlibModulesImportStandalone(t *testing.T) {
	var modules []string
	err := fs.WalkDir(stdlib.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".fern") {
			return nil
		}
		mod := strings.TrimSuffix(path, ".fern") // e.g. "std/array", "core/int"
		base := filepath.Base(mod)
		// _test_empty is a fixture with nothing to check.
		if strings.HasPrefix(base, "_test") {
			return nil
		}
		modules = append(modules, mod)
		return nil
	})
	if err != nil {
		t.Fatalf("walk stdlib FS: %v", err)
	}
	if len(modules) == 0 {
		t.Fatal("no stdlib modules discovered")
	}

	for _, mod := range modules {
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
