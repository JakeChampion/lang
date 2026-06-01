package modload_test

// LoadWith in-memory overrides — the LSP "unsaved buffer" path.
//
// LoadWith threads an override map (canonical path → source) through
// the loader; readSource consults it before touching disk, so an
// editor's in-flight buffer is the source of truth while a file is
// open even though it hasn't been saved. LoadSource (REPL / stdin /
// playground / wasm) is a thin wrapper over the same mechanism. None
// of this had a direct test, despite being the entry point every
// in-memory compile path runs through.
//
// These pin three properties of the override contract:
//   - an override shadows the on-disk file at the same path;
//   - a module supplied only as an override (no file on disk) loads;
//   - an overridden entry still pulls real on-disk imports, and
//     mangling / qualified-call rewriting works across the mix.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/modload"
)

// TestLoadWithOverrideShadowsDisk: when an override is supplied for a
// path that also exists on disk, the override wins.
func TestLoadWithOverrideShadowsDisk(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `function main(): i32 { return 1; }`,
	})
	entry := filepath.Join(dir, "main.fern")
	entryAbs, _ := filepath.Abs(entry)

	// The buffer differs from disk (returns 2, not 1) and adds a
	// helper. If the loader read disk instead, `helper` wouldn't exist.
	overrides := map[string]string{
		entryAbs: `function helper(): i32 { return 2; }
function main(): i32 { return helper(); }`,
	}
	prog, _, err := modload.LoadWith(entry, overrides)
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if findFunc(prog, "helper") == nil {
		t.Errorf("override should shadow disk (expected `helper` from the buffer); got %v", funcNames(prog))
	}
}

// TestLoadWithOverrideOnlyNoDiskFile: a path that exists *only* as an
// override (never written to disk) loads from the buffer.
func TestLoadWithOverrideOnlyNoDiskFile(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "ghost.fern") // never created on disk
	entryAbs, _ := filepath.Abs(entry)

	if _, statErr := os.Stat(entry); statErr == nil {
		t.Fatalf("precondition: %s should not exist on disk", entry)
	}
	overrides := map[string]string{
		entryAbs: `function main(): i32 { return 42; }`,
	}
	prog, _, err := modload.LoadWith(entry, overrides)
	if err != nil {
		t.Fatalf("LoadWith of an override-only path: %v", err)
	}
	if findFunc(prog, "main") == nil {
		t.Errorf("expected `main` loaded from the override buffer; got %v", funcNames(prog))
	}
}

// TestLoadWithOverriddenEntryImportsRealFile: the common LSP shape —
// the open file (overridden) imports a sibling that's only on disk.
// The override path and the disk path must compose: the import
// resolves, the dependency's decl is mangled, and the qualified call
// is rewritten to the flat name.
func TestLoadWithOverriddenEntryImportsRealFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.fern": `pub function answer(): i32 { return 42; }`,
		// main.fern on disk is a stale stub; the buffer below
		// supersedes it and adds the import + call.
		"main.fern": `function main(): i32 { return 0; }`,
	})
	entry := filepath.Join(dir, "main.fern")
	entryAbs, _ := filepath.Abs(entry)
	overrides := map[string]string{
		entryAbs: `import "./util";
function main(): i32 { return util.answer(); }`,
	}
	prog, _, err := modload.LoadWith(entry, overrides)
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if findFunc(prog, "util__answer") == nil {
		t.Errorf("on-disk import should still load + mangle as util__answer; got %v", funcNames(prog))
	}
	main := findFunc(prog, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	if !callsDirect(main, "util__answer") {
		t.Errorf("buffer's `util.answer()` should rewrite to util__answer")
	}
}
