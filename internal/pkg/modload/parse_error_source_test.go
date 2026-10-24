package modload_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestLoadReturnsSourceOnParseError(t *testing.T) {
	const src = "function f32(): i32 { return 1; }\nfunction main(): i32 { return 0; }\n"
	dir := writeFiles(t, map[string]string{"main.fern": src})
	entry := filepath.Join(dir, "main.fern")

	prog, srcs, _, err := modload.LoadWithLiterate(entry, nil)
	if err == nil {
		t.Fatal("expected a parse error for a function named `f32`")
	}
	if prog != nil {
		t.Errorf("expected nil program on parse error, got non-nil")
	}
	if srcs == nil {
		t.Fatal("srcs was nil on parse error — the source map must survive so diagnostics can render the offending line")
	}
	abs, _ := filepath.Abs(entry)
	if got := srcs[abs]; got != src {
		t.Fatalf("srcs[entry] = %q, want the full entry source", got)
	}

	out := diag.Format(entry, srcs[abs], err)
	if !strings.Contains(out, "function f32(): i32") {
		t.Errorf("diagnostic did not render the offending source line:\n%s", out)
	}
}

func TestParseErrorNamesTheModuleItCameFrom(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern":   "import \"./helper\";\nfunction main(): i32 { return helper.go(); }\n",
		"helper.fern": "pub function go(): i32 {\n    return 1 +;\n}\n",
	})
	entry := filepath.Join(dir, "main.fern")
	helper, _ := filepath.Abs(filepath.Join(dir, "helper.fern"))

	_, srcs, err := modload.Load(entry)
	if err == nil {
		t.Fatal("expected a parse error from the imported module")
	}
	es, ok := err.(diag.Errors)
	if !ok {
		t.Fatalf("expected diag.Errors, got %T", err)
	}
	for i, one := range es {
		f, ok := one.(diag.Filed)
		if !ok {
			t.Fatalf("error %d (%T) carries no file attribution at all", i, one)
		}
		if f.File() != helper {
			t.Fatalf("error %d attributed to %q, want the module it was parsed from (%q)", i, f.File(), helper)
		}
	}

	out := diag.Format(helper, srcs[helper], err)
	if !strings.Contains(out, "return 1 +;") {
		t.Errorf("diagnostic did not render the imported module's offending line:\n%s", out)
	}
}
