package caps_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/caps"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

func writeTree(t *testing.T, files map[string]string) string {
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

func loadChecked(t *testing.T, entry string) *ast.Program {
	t.Helper()
	prog, _, err := modload.Load(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatal(err)
	}
	return prog
}

// rootOnly folds everything (stdlib included) into a single "(root)"
// row, the manifest-less shape.
func rootOnly(module string) string {
	if strings.HasPrefix(module, "stdlib://") {
		return ""
	}
	return "(root)"
}

// A capability reached only via a transitive callee in another module
// (here: another package) is attributed to the calling package with
// the full example chain — and the declaring package reports it too.
func TestAnalyzeTransitiveAcrossModules(t *testing.T) {
	root := writeTree(t, map[string]string{
		"main.fern": `import "./util";
function main(): i32 {
  util.read_it();
  return 0;
}`,
		"util.fern": `pub function read_it(): void {
  read_file("/etc/hostname");
}`,
	})
	prog := loadChecked(t, filepath.Join(root, "main.fern"))
	pkgOf := func(module string) string {
		switch {
		case strings.HasPrefix(module, "stdlib://"):
			return ""
		case strings.HasSuffix(module, "util.fern"):
			return "util"
		default:
			return "(root)"
		}
	}
	rows := caps.Analyze(prog, pkgOf)
	if len(rows) != 2 || rows[0].Package != "(root)" || rows[1].Package != "util" {
		t.Fatalf("want (root) + util rows, got %+v", rows)
	}
	if len(rows[0].Uses) != 1 || rows[0].Uses[0].Capability != "fs" {
		t.Fatalf("want fs only on (root), got %+v", rows[0].Uses)
	}
	wantChain := []string{"main", "util__read_it", "read_file"}
	if got := rows[0].Uses[0].Chain; strings.Join(got, "|") != strings.Join(wantChain, "|") {
		t.Fatalf("(root) chain: got %v, want %v", got, wantChain)
	}
	if len(rows[1].Uses) != 1 || strings.Join(rows[1].Uses[0].Chain, "|") != "util__read_it|read_file" {
		t.Fatalf("util chain: got %+v", rows[1].Uses)
	}
}

// A closure counts at its DEFINITION package: the app's lambda calls
// tcp_connect and is handed to the helper package, which invokes it.
// `net` lands on app's row; helper — which only calls the closure
// value — reports nothing.
func TestAnalyzeClosureDefinitionSiteAttribution(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
		"app/main.fern": `import "helper";
function main(): i32 {
  return helper.run(function (host: i32): i32 {
    return tcp_connect(host, 80);
  });
}`,
		"helper/fern.toml": "[package]\nname = \"helper\"\n",
		"helper/lib.fern": `pub function run(f: (i32) => i32): i32 {
  return f(1);
}`,
	})
	prog := loadChecked(t, filepath.Join(root, "app", "main.fern"))
	pkgOf := func(module string) string {
		switch {
		case strings.HasPrefix(module, "stdlib://"):
			return ""
		case strings.Contains(module, string(filepath.Separator)+"helper"+string(filepath.Separator)):
			return "helper"
		default:
			return "app"
		}
	}
	rows := caps.Analyze(prog, pkgOf)
	if len(rows) != 2 {
		t.Fatalf("want app + helper rows, got %+v", rows)
	}
	app, helper := rows[0], rows[1]
	if app.Package != "app" || helper.Package != "helper" {
		t.Fatalf("row order: got %q, %q", app.Package, helper.Package)
	}
	if len(app.Uses) != 1 || app.Uses[0].Capability != "net" {
		t.Fatalf("app should report net (closure defined there), got %+v", app.Uses)
	}
	wantChain := []string{"main", "tcp_connect"}
	if got := app.Uses[0].Chain; strings.Join(got, "|") != strings.Join(wantChain, "|") {
		t.Fatalf("app chain: got %v, want %v", got, wantChain)
	}
	if len(helper.Uses) != 0 {
		t.Fatalf("helper only invokes the caller's closure; want no uses, got %+v", helper.Uses)
	}
}

// Declared reachability: an uncalled-but-declared function still
// counts toward its package — Phase 1 reports what the package's code
// could do, not just this program's live paths.
func TestAnalyzeUncalledDeclaredFunctionCounts(t *testing.T) {
	root := writeTree(t, map[string]string{
		"main.fern": `function main(): i32 { return 0; }
function stray(): i32 {
  return tcp_connect(1, 2);
}`,
	})
	prog := loadChecked(t, filepath.Join(root, "main.fern"))
	rows := caps.Analyze(prog, rootOnly)
	if len(rows) != 1 || len(rows[0].Uses) != 1 || rows[0].Uses[0].Capability != "net" {
		t.Fatalf("stray() is declared, so net must be reported: %+v", rows)
	}
	wantChain := []string{"stray", "tcp_connect"}
	if got := rows[0].Uses[0].Chain; strings.Join(got, "|") != strings.Join(wantChain, "|") {
		t.Fatalf("chain: got %v, want %v", got, wantChain)
	}
}

// A function value passed by name (not called directly) still creates
// a reachability edge.
func TestAnalyzeFunctionValueEdge(t *testing.T) {
	root := writeTree(t, map[string]string{
		"main.fern": `function probe(host: i32): i32 {
  return tcp_connect(host, 80);
}
function apply(f: (i32) => i32): i32 {
  return f(1);
}
function main(): i32 {
  return apply(probe);
}`,
	})
	prog := loadChecked(t, filepath.Join(root, "main.fern"))
	rows := caps.Analyze(prog, rootOnly)
	if len(rows) != 1 || len(rows[0].Uses) != 1 || rows[0].Uses[0].Capability != "net" {
		t.Fatalf("want net via the probe function value: %+v", rows)
	}
}

func TestFormat(t *testing.T) {
	rows := []caps.Row{
		{Package: "app", Uses: []caps.Use{
			{Capability: "fs", Chain: []string{"main", "lib__save", "write_file"}},
			{Capability: "net", Chain: []string{"main", "fetch__fetch_raw", "tcp_connect"}},
		}},
		{Package: "helper", Uses: nil},
	}
	got := caps.Format(rows)
	want := "app  fs,net  (example: main → lib__save → write_file)\n" +
		"helper  -\n"
	if got != want {
		t.Fatalf("caps.Format:\ngot  %q\nwant %q", got, want)
	}
}
