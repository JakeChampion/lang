package checker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

// loadCheckFiles is checkFiles for the cases whose diagnostic comes from the
// loader: a load error is returned rather than failing the test.
func loadCheckFiles(t *testing.T, files map[string]string, entry string) error {
	t.Helper()
	dir := t.TempDir()
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, entry))
	if err != nil {
		return err
	}
	_, err = Check(prog)
	return err
}

// `mod.Variant` names a variant of one of mod's exported enums, in
// expression position (a constructor call or a payload-less value) and in
// pattern position, nested to any depth (#10430). Every case is
// multi-module because the spelling only exists across a module boundary.
func TestModuleQualifiedVariant(t *testing.T) {
	const lib = "pub enum Shape { Circle(i32), Unit, Other(i32) }\n" +
		"pub function area(s: Shape): i32 { match (s) { Circle(r) => { return r * r; }, Unit => { return 1; }, Shape.Other(n) => { return n; } } }\n" +
		"pub function Unit(): i32 { return 7; }\n"

	t.Run("constructs and matches through the module", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"lib.fern": lib,
			"main.fern": "import \"./lib\";\n" +
				"function main(): i32 {\n" +
				"    let c: lib.Shape = lib.Circle(3);\n" +
				// `Other` is also an IoError variant, so the module form is
				// the only spelling that reaches lib's.
				"    let o: lib.Shape = lib.Other(5);\n" +
				"    let total: i32 = 0;\n" +
				"    match (c) { lib.Circle(r) => { total = total + r; }, _ => { return 1; } }\n" +
				"    match (o) { lib.Other(n) => { total = total + n; }, _ => { return 2; } }\n" +
				"    match (Some(o)) { Some(lib.Other(n)) => { total = total + n; }, _ => { return 3; } }\n" +
				"    match ((c, 1)) { (lib.Circle(r), 1) => { total = total + r; }, _ => { return 4; } }\n" +
				"    return total + lib.area(o);\n" +
				"}\n",
		}, "main.fern")
		if err != nil {
			t.Fatalf("mod.Variant must resolve in every position:\n%v", err)
		}
	})

	t.Run("a public function of the same name wins", func(t *testing.T) {
		// lib exports both a payload-less variant `Unit` and a function
		// `Unit()`: the call is the function, as a bare name would be.
		err, _ := checkFiles(t, map[string]string{
			"lib.fern": lib,
			"main.fern": "import \"./lib\";\n" +
				"function main(): i32 { return lib.Unit(); }\n",
		}, "main.fern")
		if err != nil {
			t.Fatalf("lib.Unit() is lib's function:\n%v", err)
		}
	})

	t.Run("a private enum's variant is reported as not exported", func(t *testing.T) {
		err := loadCheckFiles(t, map[string]string{
			"lib.fern": "enum Hidden { H(i32) }\npub function q(): i32 { return 0; }\n",
			"main.fern": "import \"./lib\";\n" +
				"function main(): i32 { let h = lib.H(1); return lib.q(); }\n",
		}, "main.fern")
		if err == nil {
			t.Fatal("expected an error for a private enum's variant")
		}
		if !strings.Contains(err.Error(), "variant of enum Hidden, which is not exported") {
			t.Errorf("want the private-enum wording, got:\n%v", err)
		}
	})

	t.Run("a private enum's variant in a pattern is reported the same way", func(t *testing.T) {
		err := loadCheckFiles(t, map[string]string{
			"lib.fern": "enum Hidden { H(i32) }\npub function q(): i32 { return 0; }\n",
			"main.fern": "import \"./lib\";\n" +
				"function main(): i32 { match (Some(1)) { Some(lib.H(n)) => { return n; }, _ => { return 0; } } }\n",
		}, "main.fern")
		if err == nil {
			t.Fatal("expected an error for a private enum's variant")
		}
		if !strings.Contains(err.Error(), "variant of enum Hidden, which is not exported") {
			t.Errorf("want the private-enum wording, got:\n%v", err)
		}
	})

	t.Run("a name no enum declares still reports the function path", func(t *testing.T) {
		err := loadCheckFiles(t, map[string]string{
			"lib.fern": lib,
			"main.fern": "import \"./lib\";\n" +
				"function main(): i32 { let s = lib.Square(2); return 0; }\n",
		}, "main.fern")
		if err == nil || !strings.Contains(err.Error(), "module \"lib\" has no function \"Square\"") {
			t.Errorf("want the unchanged no-such-function report, got:\n%v", err)
		}
	})

	t.Run("a variant two exported enums share is refused by name", func(t *testing.T) {
		err := loadCheckFiles(t, map[string]string{
			"lib.fern": "pub enum A { Same }\npub enum B { Same }\npub function q(): i32 { return 0; }\n",
			"main.fern": "import \"./lib\";\n" +
				"function main(): i32 { let a: lib.A = lib.Same; return 0; }\n",
		}, "main.fern")
		if err == nil || !strings.Contains(err.Error(), "variant of more than one exported enum") {
			t.Errorf("want the shared-variant report, got:\n%v", err)
		}
	})

	t.Run("the module qualifier must name the enum's module", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"lib.fern":   lib,
			"other.fern": "pub enum Shape { Circle(i32) }\n",
			"main.fern": "import \"./lib\";\nimport \"./other\";\n" +
				"function main(): i32 { let c: lib.Shape = lib.Circle(1); match (c) { other.Circle(r) => { return r; }, _ => { return 0; } } }\n",
		}, "main.fern")
		if err == nil || !strings.Contains(err.Error(), "variant pattern qualifier names module") {
			t.Errorf("want E029 for a qualifier naming the wrong module, got:\n%v", err)
		}
	})

	t.Run("the E036 hint spells the module form for an imported enum", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"lib.fern": lib,
			"main.fern": "import \"./lib\";\n" +
				"function main(): i32 { let o: lib.Shape = Other(1); return 0; }\n",
		}, "main.fern")
		if err == nil {
			t.Fatal("expected E036 for the ambiguous bare variant")
		}
		msg := err.Error()
		for _, want := range []string{"`IoError.Other(...)`", "`lib.Other(...)`"} {
			if !strings.Contains(msg, want) {
				t.Errorf("hint should offer %s:\n%s", want, msg)
			}
		}
	})
}
