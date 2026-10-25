package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/tools/gates"
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

func TestCapabilitiesReportPathDep(t *testing.T) {
	root := writeCapsTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
		"app/main.fern": `import "std/net";
import "helper";
function main(): i32 {
  match (net.connect(net.socket_addr(net.ipv4_loopback(), 8080))) {
    Ok(sock) => { helper.save("connected"); },
    Err(e) => {},
  }
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

func runEnforce(t *testing.T, entry string) (error, string) {
	t.Helper()
	e, err := loadEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := constfold.Fold(e.prog, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(e.prog); err != nil {
		t.Fatal(err)
	}
	ws, err := gates.Capabilities(entry, e.prog)
	var warns strings.Builder
	for _, w := range ws {
		warns.WriteString(w.Format(entry) + "\n")
	}
	return err, warns.String()
}

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
  let fd: i32 = tcp_close(0);
  write_file("/tmp/fern-caps-out.txt", s);
}`,
	})
	entry := filepath.Join(root, "app", "main.fern")
	err, warns := runEnforce(t, entry)
	if err == nil {
		t.Fatal("expected an E070 violation, got none")
	}
	want := `package "helper" reaches 'net' (tcp_close) without a capability grant: save → tcp_close; add "net" to its capabilities in fern.toml or remove the call`
	if got := err.Error(); !strings.Contains(got, want) {
		t.Errorf("error:\ngot  %q\nwant substring %q", got, want)
	}
	var ce *checker.Error
	if errs, ok := err.(diag.Errors); !ok || len(errs) != 1 || !errors.As(errs[0], &ce) ||
		!strings.HasSuffix(ce.Path, filepath.Join("helper", "lib.fern")) || ce.Pos.Line != 1 || ce.Pos.Col != 5 {
		t.Errorf("E070 should sit at helper/lib.fern:1:5, got %+v", ce)
	}
	if warns != "" {
		t.Errorf("governed package should error, not warn: %q", warns)
	}
	if cerr := runCheck(entry, ""); cerr == nil || !strings.Contains(cerr.Error(), "E070") {
		t.Errorf("runCheck should fail with E070, got: %v", cerr)
	}
	if code, ierr := runInterp(entry, nil); ierr == nil || !strings.Contains(ierr.Error(), "E070") {
		t.Errorf("runInterp should fail with E070, got code=%d err=%v", code, ierr)
	}
}

func TestEnforceCapabilitiesTraitDefaultChain(t *testing.T) {
	root := writeCapsTree(t, capsTraitDefaultTree)
	err, _ := runEnforce(t, filepath.Join(root, "app", "main.fern"))
	want := `package "b" reaches 'fs' (read_file) without a capability grant: grab → read_file;`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
	var ce *checker.Error
	if errs, ok := err.(diag.Errors); !ok || len(errs) != 1 || !errors.As(errs[0], &ce) ||
		!strings.HasSuffix(ce.Path, filepath.Join("b", "lib.fern")) || ce.Pos.Line != 3 || ce.Pos.Col != 3 {
		t.Errorf("E070 should sit at b/lib.fern:3:3, got %+v", ce)
	}
}

var capsTraitDefaultTree = map[string]string{
	"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nb = { path = \"../b\", capabilities = [] }\n",
	"app/main.fern": `import "b";
struct R { n: i32 }
impl b.Leaky for R {
  function size(self: Self): i32 { return self.n; }
}
function main(): i32 {
  let r: R = R { n: 1 };
  return r.grab();
}`,
	"b/fern.toml": "[package]\nname = \"b\"\n",
	"b/lib.fern": `pub trait Leaky {
  function size(self: Self): i32;
  function grab(self: Self): i32 {
    if let Ok(t) = read_file("/etc/hostname") {
      return t.len();
    }
    return self.size();
  }
}`,
}

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
	want := filepath.Join(root, "helper", "lib.fern") + ":1:5: warning[W002]: package \"helper\" reaches 'fs' (write_file) but no capabilities key governs it: save → write_file; add capabilities = [...] to its dependency entry in fern.toml (ungoverned packages will become errors once default-deny lands)\n"
	if warns != want {
		t.Errorf("warnings:\ngot  %q\nwant %q", warns, want)
	}
	if cerr := runCheck(entry, ""); cerr != nil {
		t.Errorf("runCheck should succeed for an ungoverned dep, got: %v", cerr)
	}
}

func TestEnforceCapabilitiesRootSilent(t *testing.T) {
	root := writeCapsTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n",
		"app/main.fern": `function main(): i32 {
  let fd: i32 = tcp_close(0);
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

func TestEnforceCapabilitiesTransitiveAttribution(t *testing.T) {
	root := writeCapsTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\", capabilities = [\"fs\"] }\n",
		"app/main.fern": `import "a";
function main(): i32 {
  a.fetch("x");
  return 0;
}`,
		"a/fern.toml": "[package]\nname = \"a\"\nlib = \"a.fern\"\n[dependencies]\nb = { path = \"../b\" }\n",
		"a/a.fern": `import "b";
pub function fetch(s: string): void {
  write_file("/tmp/fern-caps-out.txt", s);
  b.send(s);
}`,
		"b/fern.toml": "[package]\nname = \"b\"\nlib = \"b.fern\"\n",
		"b/b.fern": `pub function send(s: string): void {
  let fd: i32 = tcp_close(0);
}`,
	})
	err, warns := runEnforce(t, filepath.Join(root, "app", "main.fern"))
	if err == nil {
		t.Fatal("expected a's transitive net reach to violate, got none")
	}
	want := `package "a" reaches 'net' (tcp_close) without a capability grant: fetch → send → tcp_close; add "net" to its capabilities in fern.toml or remove the call`
	if got := err.Error(); !strings.Contains(got, want) {
		t.Errorf("error:\ngot  %q\nwant substring %q", got, want)
	}
	if strings.Contains(err.Error(), `package "b"`) {
		t.Errorf("b is ungoverned (warn-and-allow) and must not be an error: %q", err.Error())
	}
	wantWarn := filepath.Join(root, "b", "b.fern") + ":1:5: warning[W002]: package \"b\" reaches 'net' (tcp_close) but no capabilities key governs it: send → tcp_close; add capabilities = [...] to its dependency entry in fern.toml (ungoverned packages will become errors once default-deny lands)\n"
	if warns != wantWarn {
		t.Errorf("warnings:\ngot  %q\nwant %q", warns, wantWarn)
	}
}

func TestEnforceCapabilitiesAttenuationSurfacesInCheck(t *testing.T) {
	root := writeCapsTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\", capabilities = [\"fs\"] }\n",
		"app/main.fern": `import "a";
function main(): i32 { return a.one(); }`,
		"a/fern.toml": "[package]\nname = \"a\"\nlib = \"a.fern\"\n[dependencies]\nb = { path = \"../b\", capabilities = [\"net\"] }\n",
		"a/a.fern": `import "b";
pub function one(): i32 { return b.one(); }`,
		"b/fern.toml": "[package]\nname = \"b\"\nlib = \"b.fern\"\n",
		"b/b.fern":    "pub function one(): i32 { return 1; }",
	})
	err := runCheck(filepath.Join(root, "app", "main.fern"), "")
	if err == nil {
		t.Fatal("expected the amplifying grant to fail -check, got none")
	}
	want := `dependency "b" of "a" is granted 'net' but "a" itself holds only [fs] (attenuation: a dependency may grant at most what it holds)`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error:\ngot  %q\nwant substring %q", err.Error(), want)
	}
}

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
