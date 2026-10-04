package modload

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/diag"
)

// A refused reference into an imported module carries its position and the
// file it is written in, so the CLI renders it with a header and caret and
// the LSP publishes it where it is rather than at the top of the entry.
func TestImportErrorIsPlacedInTheReferencingModule(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{
		"main.fern": "import \"./a\";\nfunction main(): i32 {\n  return a.x();\n}\n",
		"a.fern":    "import \"./lib\";\npub function x(): i32 {\n  return lib.hidden();\n}\n",
		"lib.fern":  "function hidden(): i32 { return 1; }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, srcs, err := Load(filepath.Join(dir, "main.fern"))
	if err == nil {
		t.Fatal("want the private reference refused")
	}
	f, ok := err.(diag.Filed)
	if !ok || f.File() != filepath.Join(dir, "a.fern") {
		t.Fatalf("want it filed in a.fern, got %T %v", err, err)
	}
	p, ok := err.(diag.Positioned)
	if !ok || p.Position().Line != 3 || p.Position().Col != 13 {
		t.Fatalf("want it at 3:13, got %v", err)
	}
	got := diag.Format("a.fern", srcs[f.File()], err)
	want := "a.fern:3:13: error: lib.hidden is not exported (declare it as `pub function hidden …` to make it accessible from other modules)\n      return lib.hidden();\n                ^"
	if got != want {
		t.Errorf("rendered\n%s\nwant\n%s", got, want)
	}
}

// With refusals in two modules, the load reports the same one every run: the
// first in path order.
func TestImportErrorIsTheSameEveryRun(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{
		"main.fern": "import \"./a\";\nimport \"./b\";\nfunction main(): i32 {\n  return a.x() + b.y();\n}\n",
		"a.fern":    "import \"./c\";\npub function x(): i32 { return c.p(); }\n",
		"b.fern":    "import \"./c\";\npub function y(): i32 { return c.q(); }\n",
		"c.fern":    "function p(): i32 { return 1; }\nfunction q(): i32 { return 2; }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 30; i++ {
		_, _, err := Load(filepath.Join(dir, "main.fern"))
		f, ok := err.(diag.Filed)
		if !ok || f.File() != filepath.Join(dir, "a.fern") {
			t.Fatalf("run %d reported %v, want a.fern's refusal", i, err)
		}
	}
}
