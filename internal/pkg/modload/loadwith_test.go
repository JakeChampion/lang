package modload_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestLoadWithOverrideShadowsDisk(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `function main(): i32 { return 1; }`,
	})
	entry := filepath.Join(dir, "main.fern")
	entryAbs, _ := filepath.Abs(entry)

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

func TestLoadWithOverrideOnlyNoDiskFile(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "ghost.fern")
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

func TestLoadWithOverriddenEntryImportsRealFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.fern": `pub function answer(): i32 { return 42; }`,
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
