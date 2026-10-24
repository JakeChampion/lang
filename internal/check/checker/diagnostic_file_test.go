package checker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

func checkFiles(t *testing.T, files map[string]string, entry string) (error, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	paths := map[string]string{}
	for name, contents := range files {
		full := filepath.Join(dir, name)
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		abs, err := filepath.Abs(full)
		if err != nil {
			t.Fatal(err)
		}
		paths[name] = abs
	}
	prog, _, err := modload.Load(filepath.Join(dir, entry))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = Check(prog)
	return err, paths
}

func filesOf(t *testing.T, err error) []string {
	t.Helper()
	es, ok := err.(diag.Errors)
	if !ok {
		t.Fatalf("expected diag.Errors, got %T (%v)", err, err)
	}
	var out []string
	for i, one := range es {
		f, ok := one.(diag.Filed)
		if !ok {
			t.Fatalf("diagnostic %d (%T) carries no file attribution", i, one)
		}
		out = append(out, f.File())
	}
	return out
}

func TestNestedBodyDiagnosticsNameTheirOwnModule(t *testing.T) {
	const main = "import \"./lib\";\nfunction main(): i32 { return lib.run(); }\n"

	for _, tc := range []struct {
		name string
		lib  string
	}{
		{
			name: "lambda body",
			lib: `pub function apply(f: () => i32): i32 {
    return f();
}

pub function run(): i32 {
    return apply(() => nope);
}
`,
		},
		{
			name: "nested function body",
			lib: `pub function run(): i32 {
    function inner(): i32 {
        return nope;
    }
    return inner();
}
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err, paths := checkFiles(t, map[string]string{
				"main.fern": main,
				"lib.fern":  tc.lib,
			}, "main.fern")
			if err == nil {
				t.Fatal("expected an undefined-identifier error from the imported module")
			}
			for i, got := range filesOf(t, err) {
				if got != paths["lib.fern"] {
					t.Errorf("diagnostic %d attributed to %q, want the module that raised it (%q)",
						i, got, paths["lib.fern"])
				}
			}
		})
	}
}
