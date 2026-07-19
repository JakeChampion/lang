package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
)

func writeCapsTree(t *testing.T, files map[string]string) string {
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

// The report attributes stdlib usage to the calling package (app gets
// `net` via std/fetch → tcp_connect), gives a path dependency its own
// row (helper uses fs directly), and includes the dependency's
// capabilities in the caller's row too — the walk crosses package
// boundaries, mirroring the brief's enforcement rule. Golden output:
// deterministic, sorted by package name.
func TestCapabilitiesReportPathDep(t *testing.T) {
	root := writeCapsTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
		"app/main.fern": `import "std/fetch";
import "helper";
function main(): i32 {
  var body: string = fetch.fetch_get(fetch.ipv4(127, 0, 0, 1), 8080, "/");
  helper.save(body);
  return 0;
}`,
		"helper/fern.toml": "[package]\nname = \"helper\"\n",
		"helper/lib.fern": `pub function save(s: string): void {
  write_file("/tmp/fern-caps-out.txt", s);
}`,
	})
	var out strings.Builder
	if err := runCapabilities(filepath.Join(root, "app", "main.fern"), &out); err != nil {
		t.Fatal(err)
	}
	want := "app  fs,net  (example: main → lib__save → write_file)\n" +
		"helper  fs  (example: lib__save → write_file)\n"
	if out.String() != want {
		t.Fatalf("report:\ngot  %q\nwant %q", out.String(), want)
	}
}

// runEnforce mirrors the pipeline seam the -check / -interp / codegen
// paths run enforcement at (load → constfold → check → enforce),
// capturing warnings in the returned builder.
func runEnforce(t *testing.T, entry string) (error, string) {
	t.Helper()
	e, err := loadEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := constfold.Fold(e.prog); err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(e.prog); err != nil {
		t.Fatal(err)
	}
	var warns strings.Builder
	return enforceCapabilities(entry, e.prog, &warns), warns.String()
}

// (a) A dependency granted exactly what it uses is clean — no error,
// no warning.
func TestEnforceCapabilitiesGrantedClean(t *testing.T) {
	root := writeCapsTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\", capabilities = [\"fs\"] }\n",
		"app/main.fern": `import "helper";
function main(): i32 {
  helper.save("x");
  return 0;
}`,
		"helper/fern.toml": "[package]\nname = \"helper\"\n",
		"helper/lib.fern": `pub function save(s: string): void {
  write_file("/tmp/fern-caps-out.txt", s);
}`,
	})
	err, warns := runEnforce(t, filepath.Join(root, "app", "main.fern"))
	if err != nil {
		t.Fatalf("granted usage should be clean, got: %v", err)
	}
	if warns != "" {
		t.Fatalf("granted usage should not warn, got: %q", warns)
	}
}

// (b) The same dependency reaching outside its grant is an E070 error
// carrying the exact chain.
func TestEnforceCapabilitiesViolation(t *testing.T) {
	root := writeCapsTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\", capabilities = [\"fs\"] }\n",
		"app/main.fern": `import "helper";
function main(): i32 {
  helper.save("x");
  return 0;
}`,
		"helper/fern.toml": "[package]\nname = \"helper\"\n",
		"helper/lib.fern": `pub function save(s: string): void {
  var fd: i32 = tcp_connect(0, 80);
  write_file("/tmp/fern-caps-out.txt", s);
}`,
	})
	entry := filepath.Join(root, "app", "main.fern")
	err, warns := runEnforce(t, entry)
	if err == nil {
		t.Fatal("expected an E070 violation, got none")
	}
	want := `package "helper" reaches 'net' (tcp_connect) without a capability grant: lib__save → tcp_connect; add "net" to its capabilities in fern.toml or remove the call`
	if got := err.Error(); !strings.Contains(got, want) {
		t.Errorf("error:\ngot  %q\nwant substring %q", got, want)
	}
	if warns != "" {
		t.Errorf("governed package should error, not warn: %q", warns)
	}
	// Wiring: -check and -interp both surface the violation.
	if cerr := runCheck(entry); cerr == nil || !strings.Contains(cerr.Error(), "E070") {
		t.Errorf("runCheck should fail with E070, got: %v", cerr)
	}
	if code, ierr := runInterp(entry, nil); ierr == nil || !strings.Contains(ierr.Error(), "E070") {
		t.Errorf("runInterp should fail with E070, got code=%d err=%v", code, ierr)
	}
}

// (c) A dependency with NO capabilities key is warn-and-allow: the
// compile succeeds and each package+capability warns once with an
// example chain.
func TestEnforceCapabilitiesUngovernedWarns(t *testing.T) {
	root := writeCapsTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
		"app/main.fern": `import "helper";
function main(): i32 {
  helper.save("x");
  return 0;
}`,
		"helper/fern.toml": "[package]\nname = \"helper\"\n",
		"helper/lib.fern": `pub function save(s: string): void {
  write_file("/tmp/fern-caps-out.txt", s);
}`,
	})
	entry := filepath.Join(root, "app", "main.fern")
	err, warns := runEnforce(t, entry)
	if err != nil {
		t.Fatalf("ungoverned usage must not error (warn-and-allow), got: %v", err)
	}
	want := "warning: package \"helper\" reaches 'fs' (write_file) but no capabilities key governs it: lib__save → write_file; add capabilities = [...] to its dependency entry in fern.toml (ungoverned packages will become errors once default-deny lands)\n"
	if warns != want {
		t.Errorf("warnings:\ngot  %q\nwant %q", warns, want)
	}
	if cerr := runCheck(entry); cerr != nil {
		t.Errorf("runCheck should succeed for an ungoverned dep, got: %v", cerr)
	}
}

// (d) The root package is unrestricted: capability usage in the root
// is silent — no error, no warning — even with a manifest present.
func TestEnforceCapabilitiesRootSilent(t *testing.T) {
	root := writeCapsTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n",
		"app/main.fern": `function main(): i32 {
  var fd: i32 = tcp_connect(0, 80);
  write_file("/tmp/fern-caps-out.txt", "x");
  return 0;
}`,
		"bare.fern": `function main(): i32 {
  sleep_ms(1);
  return 0;
}`,
	})
	for _, entry := range []string{filepath.Join(root, "app", "main.fern"), filepath.Join(root, "bare.fern")} {
		err, warns := runEnforce(t, entry)
		if err != nil || warns != "" {
			t.Errorf("%s: root usage must be silent, got err=%v warns=%q", entry, err, warns)
		}
	}
}

// (e) Transitive: a dependency whose OWN dependency reaches net is
// held to its own grant — the violation attributes to every governed
// package that reaches the builtin, with the cross-package chain.
// Here `a` (granted fs only) reaches net through `b` (granted net by
// a's manifest, so b itself is clean).
func TestEnforceCapabilitiesTransitiveAttribution(t *testing.T) {
	root := writeCapsTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\", capabilities = [\"fs\"] }\n",
		"app/main.fern": `import "a";
function main(): i32 {
  a.fetch("x");
  return 0;
}`,
		"a/fern.toml": "[package]\nname = \"a\"\nlib = \"a.fern\"\n[dependencies]\nb = { path = \"../b\", capabilities = [\"net\"] }\n",
		"a/a.fern": `import "b";
pub function fetch(s: string): void {
  write_file("/tmp/fern-caps-out.txt", s);
  b.send(s);
}`,
		"b/fern.toml": "[package]\nname = \"b\"\nlib = \"b.fern\"\n",
		"b/b.fern": `pub function send(s: string): void {
  var fd: i32 = tcp_connect(0, 80);
}`,
	})
	err, warns := runEnforce(t, filepath.Join(root, "app", "main.fern"))
	if err == nil {
		t.Fatal("expected a's transitive net reach to violate, got none")
	}
	want := `package "a" reaches 'net' (tcp_connect) without a capability grant: a__fetch → b__send → tcp_connect; add "net" to its capabilities in fern.toml or remove the call`
	if got := err.Error(); !strings.Contains(got, want) {
		t.Errorf("error:\ngot  %q\nwant substring %q", got, want)
	}
	if strings.Contains(err.Error(), `package "b"`) {
		t.Errorf("b holds a net grant and must not be reported: %q", err.Error())
	}
	if warns != "" {
		t.Errorf("all packages governed; no warnings expected, got %q", warns)
	}
}

// No fern.toml anywhere: a single `(root)` row.
func TestCapabilitiesReportNoManifest(t *testing.T) {
	root := writeCapsTree(t, map[string]string{
		"main.fern": `function main(): i32 {
  sleep_ms(1);
  return 0;
}`,
	})
	var out strings.Builder
	if err := runCapabilities(filepath.Join(root, "main.fern"), &out); err != nil {
		t.Fatal(err)
	}
	want := "(root)  time  (example: main → sleep_ms)\n"
	if out.String() != want {
		t.Fatalf("report:\ngot  %q\nwant %q", out.String(), want)
	}
}
