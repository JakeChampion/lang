package manifest

import (
	"os"
	"path/filepath"
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
		"malformed version": "[package]\nname = \"a\"\n[dependencies]\nfoo = \"1.2\"\n",
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

func TestVersionDep(t *testing.T) {
	m, err := Parse("[package]\nname = \"a\"\nindex = \"reg.toml\"\n[dependencies]\nfoo = \"1.2.0\"\nbar = { version = \"2.0.1\" }\n")
	if err != nil {
		t.Fatal(err)
	}
	if m.Index != "reg.toml" {
		t.Errorf("index = %q", m.Index)
	}
	if m.Deps["foo"].Version != "1.2.0" || m.Deps["bar"].Version != "2.0.1" {
		t.Errorf("version deps parsed wrong: %+v", m.Deps)
	}
	if got := m.VersionDeps(); got["foo"] != "1.2.0" || got["bar"] != "2.0.1" || len(got) != 2 {
		t.Errorf("VersionDeps = %v", got)
	}
}

func TestVersionDepConflicts(t *testing.T) {
	for label, src := range map[string]string{
		"version+path": "[package]\nname = \"a\"\n[dependencies]\nfoo = { version = \"1.0.0\", path = \"../x\" }\n",
		"version+url":  "[package]\nname = \"a\"\n[dependencies]\nfoo = { version = \"1.0.0\", url = \"https://x/y.tar.gz\" }\n",
		"bad version":  "[package]\nname = \"a\"\n[dependencies]\nfoo = \"v1.2.3\"\n",
		"two-part":     "[package]\nname = \"a\"\n[dependencies]\nfoo = \"1.2\"\n",
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s: expected error", label)
		}
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

func TestParseExclude(t *testing.T) {
	m, err := Parse("[package]\nname = \"app\"\n[dependencies]\nbar = \"1.0.0\"\n[exclude]\nbar = [\"1.9.0\", \"1.9.1\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Excludes["bar"]; len(got) != 2 || got[0] != "1.9.0" || got[1] != "1.9.1" {
		t.Errorf("excludes = %v, want [1.9.0 1.9.1]", got)
	}
}

func TestParseExcludeErrors(t *testing.T) {
	cases := map[string]string{
		"not an array":     "[package]\nname = \"a\"\n[exclude]\nbar = \"1.9.0\"\n",
		"not a version":    "[package]\nname = \"a\"\n[exclude]\nbar = [\"1.9\"]\n",
		"invalid pkg name": "[package]\nname = \"a\"\n[exclude]\n9bar = [\"1.9.0\"]\n",
	}
	for label, src := range cases {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s: expected error", label)
		}
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
