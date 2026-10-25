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

func TestParseTrailingComments(t *testing.T) {
	m, err := Parse(`[package]  # the package
name = "app"
version = "0.1.0"  # first release
lib = "lib#1.fern"

[dependencies]
helper = { path = "../helper" }  # beside us
tagged = { path = "libs/a#b" }
`)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "app" || m.Version != "0.1.0" || m.Lib != "lib#1.fern" {
		t.Errorf("package fields: %+v", m)
	}
	if d := m.Deps["helper"]; d.Path != filepath.FromSlash("../helper") {
		t.Errorf("helper dep: %+v", d)
	}
	if d := m.Deps["tagged"]; d.Path != filepath.FromSlash("libs/a#b") {
		t.Errorf("tagged dep: %+v", d)
	}
	w, err := Parse(`[workspace]
members = ["a", "b#c"]  # two members
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Members) != 2 || w.Members[1] != "b#c" {
		t.Errorf("members: %q", w.Members)
	}
}

func TestParseDepCapabilities(t *testing.T) {
	m, err := Parse(`[package]
name = "app"
[dependencies]
kv = { path = "../kv", capabilities = ["net", "fs", "net"] }
json = { url = "https://example.com/json.tar.gz", hash = "sha256:` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `", capabilities = [] }
member = { workspace = true, capabilities = ["env"] }
plain = { path = "../plain" }
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Deps["kv"].Capabilities; len(got) != 2 || got[0] != "fs" || got[1] != "net" {
		t.Errorf("kv capabilities (want sorted+deduped [fs net]): %v", got)
	}
	if got := m.Deps["json"].Capabilities; got == nil || len(got) != 0 {
		t.Errorf("json capabilities (want non-nil empty — governed, nothing granted): %#v", got)
	}
	if got := m.Deps["member"].Capabilities; len(got) != 1 || got[0] != "env" {
		t.Errorf("member capabilities: %v", got)
	}
	if got := m.Deps["plain"].Capabilities; got != nil {
		t.Errorf("plain capabilities (want nil — key absent): %#v", got)
	}
	if kv, plain := m.Deps["kv"].Line, m.Deps["plain"].Line; kv != 4 || plain != 7 {
		t.Errorf("dependency lines: kv %d, plain %d, want 4 and 7", kv, plain)
	}
}

func TestParseDepCapabilitiesUnknownName(t *testing.T) {
	_, err := Parse("[package]\nname = \"app\"\n[dependencies]\nkv = { path = \"../kv\", capabilities = [\"network\"] }\n")
	if err == nil {
		t.Fatal("expected an unknown-capability error, got none")
	}
	want := `unknown capability "network" (valid capabilities: env, fs, net, random, subprocess, time)`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name the vocabulary (want substring %q)", err, want)
	}
}

func TestLoadErrorIsE088AtItsLine(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"[package]\nname = \"app\"\n[dependencies]\nkv = { path = \"../kv\", capabilities = [\"nett\"] }\n", ":4:1: error[E088]: dependency \"kv\": capabilities: unknown capability \"nett\""},
		{"# comment\n[package]\nversion = \"1.0.0\"\n", ":2:1: error[E088]: missing [package] name"},
		{"[dependencies]\n", ":1:1: error[E088]: missing [package] name"},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, FileName)
		if err := os.WriteFile(path, []byte(c.src), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(dir)
		me, ok := err.(*Error)
		if !ok {
			t.Fatalf("Load(%q) error = %v (%T), want a *manifest.Error", c.src, err, err)
		}
		if me.Code() != "E088" || !strings.HasPrefix(err.Error(), path+c.want) {
			t.Errorf("Load(%q) error = %q, want %q", c.src, err, path+c.want)
		}
	}
}

func TestParseDuplicates(t *testing.T) {
	for src, want := range map[string]string{
		"[package]\nname = \"a\"\n[dependencies]\n[package]\n":                                                     `4:1: error[E088]: duplicate section [package]`,
		"[package]\nname = \"a\"\nname = \"b\"\n":                                                                  `3:1: error[E088]: duplicate key "name" in [package]`,
		"[package]\nname = \"a\"\n[dependencies]\nh = { path = \"h\", capabilities = [] }\nh = { path = \"h\" }\n": `5:1: error[E088]: duplicate key "h" in [dependencies]`,
	} {
		if _, err := Parse(src); err == nil || err.Error() != want {
			t.Errorf("Parse(%q) error = %v, want %s", src, err, want)
		}
	}
	if _, err := Parse("[package]\nname = \"a\"\n[dependencies]\nname = { path = \"n\" }\n"); err != nil {
		t.Errorf("one key in two sections: %v", err)
	}
}

func TestParseDepQuotedSeparators(t *testing.T) {
	m, err := Parse("[package]\nname = \"app\"\n[dependencies]\nkv = { path = \"a,b[c\", capabilities = [\"fs\"] }\n")
	if err != nil {
		t.Fatal(err)
	}
	if d := m.Deps["kv"]; d.Path != "a,b[c" || len(d.Capabilities) != 1 || d.Capabilities[0] != "fs" {
		t.Errorf("kv = %+v, want path a,b[c with capabilities [fs]", d)
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

func TestParseLintTables(t *testing.T) {
	if _, err := Parse("[package]\nname = \"a\"\n[lint]\ncyclomatic-complexity = \"deny\"\n[lint.options]\ncyclomatic-complexity.max = 25\n"); err != nil {
		t.Fatal(err)
	}
}

func TestParseLintErrors(t *testing.T) {
	cases := map[string]string{
		"undotted option key": "[package]\nname = \"a\"\n[lint.options]\nmax = 25\n",
		"unquoted severity":   "[package]\nname = \"a\"\n[lint]\ncyclomatic-complexity = deny\n",
		"empty value":         "[package]\nname = \"a\"\n[lint]\ncyclomatic-complexity =\n",
		"array value":         "[package]\nname = \"a\"\n[lint]\ncyclomatic-complexity = [\"deny\"]\n",
	}
	for label, src := range cases {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s: expected parse error, got none", label)
		}
	}
}
