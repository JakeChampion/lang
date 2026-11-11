package checker

import (
	"os"
	"path/filepath"
	"strings"
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

// A redeclaration in an imported module names that module, for each kind of
// declaration.
func TestRedeclarationDiagnosticsNameTheirOwnModule(t *testing.T) {
	const main = "import \"./lib\";\nfunction main(): i32 { return 0; }\n"
	for _, tc := range []struct{ name, lib, want string }{
		{"struct", "pub struct R { x: i32 }\npub struct R { y: i32 }\n", `struct "lib.R" redeclared`},
		{"enum", "pub enum E { A }\npub enum E { B }\n", `enum "lib.E" redeclared`},
		{"trait", "pub trait T { }\npub trait T { }\n", `trait "lib.T" redeclared`},
		{"trait method", "pub trait T { function a(self: Self): i32; function a(self: Self): i32; }\n", `trait method "a" redeclared`},
		{"impl", "pub trait T { }\npub struct R { x: i32 }\nimpl T for R { }\nimpl T for R { }\n", "duplicate impl: lib.T is already implemented for lib.R"},
		{"method", "pub struct R { x: i32 }\nfunction (r: R) a(): i32 { return 1; }\nfunction (r: R) a(): i32 { return 2; }\n", `method "a" on lib.R redeclared`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err, paths := checkFiles(t, map[string]string{"main.fern": main, "lib.fern": tc.lib}, "main.fern")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			for i, got := range filesOf(t, err) {
				if got != paths["lib.fern"] {
					t.Errorf("diagnostic %d names %q, want %q", i, got, paths["lib.fern"])
				}
			}
		})
	}
}
