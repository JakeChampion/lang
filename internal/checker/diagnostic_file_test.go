package checker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/modload"
)

// checkFiles writes `files` into a temp dir, loads `entry` with its imports
// resolved, and type-checks the merged program. It returns the checker's
// errors plus the absolute path of each written file, so a test can assert
// which module a diagnostic is attributed to.
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

// filesOf returns the File() attribution of every diagnostic in err.
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

// TestNestedBodyDiagnosticsNameTheirOwnModule pins that an error raised inside
// a LAMBDA body or a NESTED function body is blamed on the module that wrote
// it, not on the entry file.
//
// errf fills Error.Path from c.current.SourceModule, and modload stamps
// SourceModule on prog.Funcs only. The synthetic FuncDecl the checker swaps
// c.current to for a lambda body was built without it, and a nested `function`
// never reaches prog.Funcs at all — so both bodies reported an empty path,
// which the CLI renders against the entry file. The line:column was right all
// along; only the file was wrong, so the caret landed on whatever shares that
// column in a file that is fine.
//
// The entry module is deliberately shorter than the imported one: line 6 does
// not exist in main.fern, so a regression renders a blank snippet.
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
