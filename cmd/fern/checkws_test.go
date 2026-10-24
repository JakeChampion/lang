package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCheckTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCheckWorkspaceAllOk(t *testing.T) {
	root := writeCheckTree(t, map[string]string{
		"fern.toml":       "[workspace]\nmembers = [\"lexer\", \"app\"]\n",
		"lexer/fern.toml": "[package]\nname = \"lexer\"\n",
		"lexer/lib.fern":  "pub function token(): i32 { return 1; }",
		"app/fern.toml":   "[package]\nname = \"app\"\n[dependencies]\nlexer = { workspace = true }\n",
		"app/main.fern":   `import "lexer";` + "\n" + `function main(): i32 { return lexer.token(); }`,
	})
	if err := runCheckTarget(root, ""); err != nil {
		t.Fatalf("all-valid workspace should check clean: %v", err)
	}
}

func TestCheckWorkspaceReportsBrokenMember(t *testing.T) {
	root := writeCheckTree(t, map[string]string{
		"fern.toml":       "[workspace]\nmembers = [\"lexer\", \"app\"]\n",
		"lexer/fern.toml": "[package]\nname = \"lexer\"\n",
		"lexer/lib.fern":  "pub function token(): i32 { return 1; }",
		"app/fern.toml":   "[package]\nname = \"app\"\n",
		"app/main.fern":   `function main(): i32 { return nope(); }`,
	})
	err := runCheckTarget(root, "")
	if err == nil {
		t.Fatal("a broken member should fail the workspace check")
	}
}

func TestCheckWorkspaceHonoursLibKey(t *testing.T) {
	root := writeCheckTree(t, map[string]string{
		"fern.toml":     "[workspace]\nmembers = [\"m\"]\n",
		"m/fern.toml":   "[package]\nname = \"m\"\nlib = \"api.fern\"\n",
		"m/api.fern":    "pub function f(): i32 { return 1; }",
		"m/broken.fern": "this is not valid fern",
	})
	if err := runCheckTarget(root, ""); err != nil {
		t.Fatalf("lib=api.fern member should check clean (broken.fern isn't the entry): %v", err)
	}
}

func TestCheckPlainPackageDir(t *testing.T) {
	root := writeCheckTree(t, map[string]string{
		"fern.toml": "[package]\nname = \"solo\"\n",
		"lib.fern":  "pub function f(): i32 { return 1; }",
	})
	if err := runCheckTarget(root, ""); err != nil {
		t.Fatalf("plain package dir should check its lib: %v", err)
	}
}

func TestCheckPlainPackageAppEntry(t *testing.T) {
	root := writeCheckTree(t, map[string]string{
		"fern.toml": "[package]\nname = \"app\"\n",
		"main.fern": "function main(): i32 { return 0; }",
	})
	if err := runCheckTarget(root, ""); err != nil {
		t.Fatalf("app package should check via main.fern: %v", err)
	}
}

func TestCheckSingleFileUnchanged(t *testing.T) {
	root := writeCheckTree(t, map[string]string{
		"prog.fern": "function main(): i32 { return 0; }",
	})
	if err := runCheckTarget(filepath.Join(root, "prog.fern"), ""); err != nil {
		t.Fatalf("single-file check regressed: %v", err)
	}
}

func TestCheckBareDirErrors(t *testing.T) {
	if err := runCheckTarget(t.TempDir(), ""); err == nil {
		t.Fatal("a directory with no fern.toml should error")
	}
}
