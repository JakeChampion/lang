package sourcelint

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The native backends under internal/codegen are being retired
// (docs/NATIVE-FREEZE.md, docs/NATIVE-CONVERGENCE.md §3a). Everything the
// retirement keeps — the front end, the interpreter oracle, the self-host
// harnesses — must therefore not import them, or the deletion drags a
// survivor down with it. That held only by convention until the fdlibm table
// moved out (#10676): internal/interp reached into internal/codegen for it,
// built fine, and every lane was green.
//
// This pins the boundary. Only the packages below may import an
// internal/codegen package from non-test code; each of them goes with the
// backends. The list shrinks as they are deleted and never grows.
var codegenImporters = map[string]bool{
	"cmd/dump_arm64": true,
}

const codegenImportPrefix = `github.com/jakechampion/lang/internal/codegen`

func TestOnlyRetiringPackagesImportCodegen(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	fset := token.NewFileSet()
	var offenders []string
	for _, dir := range []string{"internal", "cmd"} {
		werr := filepath.Walk(filepath.Join(root, dir), func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			if fi.IsDir() {
				if rel == filepath.Join("internal", "codegen") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if codegenImporters[filepath.ToSlash(filepath.Dir(rel))] {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return fmt.Errorf("%s: %v", rel, err)
			}
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				if p == codegenImportPrefix || strings.HasPrefix(p, codegenImportPrefix+"/") {
					offenders = append(offenders, fmt.Sprintf("%s imports %s", filepath.ToSlash(rel), p))
				}
			}
			return nil
		})
		if werr != nil {
			t.Fatalf("walk %s: %v", dir, werr)
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("a package the retirement keeps imports a native backend (%d site(s)):\n  %s\n\n"+
			"internal/codegen goes when the backends do (docs/NATIVE-FREEZE.md). Move what the\n"+
			"survivor needs out of the backend — as internal/fdlibm was — rather than importing it.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
	// The allow-list is a deletion list: an entry that no longer imports
	// codegen is stale and leaves with the import.
	for pkg := range codegenImporters {
		if !dirImportsCodegen(t, fset, filepath.Join(root, pkg)) {
			t.Errorf("%s is allowed to import internal/codegen but no longer does — remove it from codegenImporters", pkg)
		}
	}
}

func dirImportsCodegen(t *testing.T, fset *token.FileSet, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, imp := range f.Imports {
			if strings.HasPrefix(strings.Trim(imp.Path.Value, `"`), codegenImportPrefix) {
				return true
			}
		}
	}
	return false
}
