package modload_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/modload"
)

// TestLoadReturnsSourceOnParseError pins the fix for parse errors rendering a
// BLANK source line. loadCoreLit discarded its source map on any parse failure
// (`return nil, nil, nil, err`), so the CLI/LSP diagnostic formatter had no
// source to render the offending line + caret from — every syntax error printed
// just a header and a caret under an empty line, while checker errors (reached
// only after a clean parse, with the map intact) rendered correctly.
//
// loadRecursive stamps srcs[path] BEFORE parsing each file, so the failing
// file's source is already captured; the fix returns that partial map on error.
func TestLoadReturnsSourceOnParseError(t *testing.T) {
	// `f32` is a reserved type keyword, so naming a function it is a parse
	// error (P001, expected Ident) — a realistic syntax mistake.
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

	// End-to-end: the formatter must render the offending source line, not a
	// blank one, with the caret under the reserved name.
	out := diag.Format(entry, srcs[abs], err)
	if !strings.Contains(out, "function f32(): i32") {
		t.Errorf("diagnostic did not render the offending source line:\n%s", out)
	}
}

// TestParseErrorNamesTheModuleItCameFrom pins the OTHER half of rendering a
// syntax error in a multi-file program: which file it is blamed on.
//
// loadRecursive stamps every structured parse error with the module's path
// (diag.WithFile) so the CLI formatter can pick that module's source out of
// srcs and the LSP can attribute the diagnostic to the right URI. The stamp
// went nowhere: diag's mutator interface named an UNEXPORTED method, which no
// type outside diag can ever satisfy, so the assertion inside WithFile missed
// every error it was handed. Every import's syntax error came back
// unattributed and rendered against the entry file — a real line:column from
// one file printed over another file's text.
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

	// The attribution is only worth having if it selects a source: rendering
	// against srcs[helper] must quote the offending line, which is what the
	// entry file's source could never have produced.
	out := diag.Format(helper, srcs[helper], err)
	if !strings.Contains(out, "return 1 +;") {
		t.Errorf("diagnostic did not render the imported module's offending line:\n%s", out)
	}
}
