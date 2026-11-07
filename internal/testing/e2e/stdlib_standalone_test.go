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
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The exit statuses of the standalone import verdict, one for each step that
// can fail.
const (
	standaloneUnloaded = iota + 1
	standaloneUnfolded
	standaloneUnchecked
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
			out, failed := e2eharness.InProcessSourceVerdict(t, "modload.Load, constfold.Fold, checker.Check", src, func() ([]byte, int) {
				p := filepath.Join(t.TempDir(), "main.fern")
				if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
					t.Fatalf("write: %v", err)
				}
				prog, _, err := modload.Load(p)
				if err != nil {
					return []byte(err.Error()), standaloneUnloaded
				}
				if err := constfold.Fold(prog, nil); err != nil {
					return []byte(err.Error()), standaloneUnfolded
				}
				if _, err := checker.Check(prog); err != nil {
					return []byte(err.Error()), standaloneUnchecked
				}
				return nil, 0
			})
			switch failed {
			case standaloneUnloaded:
				t.Fatalf("import %q does not load standalone: %s", mod, out)
			case standaloneUnfolded:
				t.Fatalf("import %q: constfold: %s", mod, out)
			case standaloneUnchecked:
				t.Fatalf("import %q does not type-check standalone (missing a dependency import?):\n%s", mod, out)
			}
		})
	}
}
