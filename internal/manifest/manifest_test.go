package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFull(t *testing.T) {
	m, err := Parse(`# a comment
[package]
name = "myapp"
version = "0.1.0"

[dependencies]
helper = { path = "../helper" }
web-kit = { path = "libs/webkit" }
`)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "myapp" || m.Version != "0.1.0" || m.Lib != DefaultLib {
		t.Errorf("package fields: %+v", m)
	}
	if d := m.Deps["helper"]; d.Path != filepath.FromSlash("../helper") {
		t.Errorf("helper dep: %+v", d)
	}
	if d := m.Deps["web-kit"]; d.Path != filepath.FromSlash("libs/webkit") {
		t.Errorf("web-kit dep: %+v", d)
	}
}

func TestParseLib(t *testing.T) {
	m, err := Parse("[package]\nname = \"h\"\nlib = \"src/api.fern\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if m.Lib != "src/api.fern" {
		t.Errorf("lib = %q", m.Lib)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"no name":           "[package]\nversion = \"1\"\n",
		"unknown section":   "[package]\nname = \"a\"\n[features]\n",
		"unknown pkg key":   "[package]\nname = \"a\"\nedition = \"2026\"\n",
		"version-only dep":  "[package]\nname = \"a\"\n[dependencies]\nfoo = \"1.2\"\n",
		"dep unknown key":   "[package]\nname = \"a\"\n[dependencies]\nfoo = { git = \"x\" }\n",
		"dep missing path":  "[package]\nname = \"a\"\n[dependencies]\nfoo = { }\n",
		"key outside table": "name = \"a\"\n",
		"bad dep name":      "[package]\nname = \"a\"\n[dependencies]\n1foo = { path = \"x\" }\n",
		"escape in string":  "[package]\nname = \"a\\\\b\"\n",
		"malformed section": "[package\nname = \"a\"\n",
		"non-kv line":       "[package]\nname\n",
		"unquoted value":    "[package]\nname = app\n",
	}
	for label, src := range cases {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s: expected parse error, got none", label)
		}
	}
}

func TestVersionOnlyDepErrorPointsAtPathForm(t *testing.T) {
	_, err := Parse("[package]\nname = \"a\"\n[dependencies]\nfoo = \"1.2\"\n")
	if err == nil || !strings.Contains(err.Error(), "path") {
		t.Errorf("want error mentioning the path form, got %v", err)
	}
}

func TestFindForDirWalksUp(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), []byte("[package]\nname = \"r\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := FindForDir(sub)
	if err != nil {
		t.Fatal(err)
	}
	if m == nil || m.Name != "r" {
		t.Fatalf("FindForDir: %+v", m)
	}
	rootAbs, _ := filepath.Abs(root)
	if m.Dir != rootAbs {
		t.Errorf("Dir = %q, want %q", m.Dir, rootAbs)
	}
}

func TestFindForDirNone(t *testing.T) {
	m, err := FindForDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if m != nil {
		t.Errorf("expected no manifest, got %+v", m)
	}
}

func TestDepDir(t *testing.T) {
	m := &Manifest{Dir: "/proj/app", Deps: map[string]Dep{"h": {Path: filepath.FromSlash("../h")}}}
	got, ok := m.DepDir("h")
	if !ok || got != filepath.Clean("/proj/h") {
		t.Errorf("DepDir = %q, %v", got, ok)
	}
	if _, ok := m.DepDir("nope"); ok {
		t.Error("undeclared dep resolved")
	}
}

func TestParseWorkspace(t *testing.T) {
	m, err := Parse("[workspace]\nmembers = [\"compiler/lexer\", \"handlers/app\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsWorkspace() {
		t.Fatal("expected a workspace manifest")
	}
	if len(m.Members) != 2 || m.Members[0] != filepath.FromSlash("compiler/lexer") {
		t.Errorf("members = %v", m.Members)
	}
	// Workspace-only manifest needs no [package] name.
	if m.Name != "" {
		t.Errorf("unexpected name %q", m.Name)
	}
}

func TestParseWorkspaceAndPackage(t *testing.T) {
	m, err := Parse("[package]\nname = \"root\"\n[workspace]\nmembers = [\"a\"]\n[dependencies]\na = { workspace = true }\n")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "root" || !m.IsWorkspace() || !m.Deps["a"].Workspace {
		t.Errorf("combined manifest parsed wrong: %+v", m)
	}
}

func TestParseWorkspaceDepForm(t *testing.T) {
	m, err := Parse("[package]\nname = \"a\"\n[dependencies]\nb = { workspace = true }\n")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Deps["b"].Workspace || m.Deps["b"].Path != "" {
		t.Errorf("workspace dep parsed wrong: %+v", m.Deps["b"])
	}
}

func TestParseWorkspaceErrors(t *testing.T) {
	cases := map[string]string{
		"bad members type":    "[workspace]\nmembers = \"a\"\n",
		"unknown ws key":      "[workspace]\nexclude = [\"a\"]\n",
		"workspace not true":  "[package]\nname = \"a\"\n[dependencies]\nb = { workspace = false }\n",
		"workspace plus path": "[package]\nname = \"a\"\n[dependencies]\nb = { workspace = true, path = \"../b\" }\n",
	}
	for label, src := range cases {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}
}
