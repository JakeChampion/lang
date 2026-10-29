package caps_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/caps"
)

type traitDefaultCase struct {
	capability string
	builtin    string
	stmt       string
	imports    string
	// via is the stdlib wrapper between the default and the builtin, if any.
	via []string
}

var traitDefaultCases = []traitDefaultCase{
	{"env", "env", `env("HOME");`, "", nil},
	{"env", "hostname", `hostname();`, "", nil},
	{"fs", "read_file", `read_file("/etc/hostname");`, "", nil},
	{"net", "tcp_close", `tcp_close(1);`, "", nil},
	{"random", "random_i32", `rand.next_i32();`, "import \"std/rand\";\n", []string{"rand__next_i32"}},
	{"subprocess", "subprocess", `subprocess("/bin/true", [], "");`, "", nil},
	{"time", "now_unix_ms", `now_unix_ms();`, "", nil},
}

func pkgByDir(dirs ...string) func(string) string {
	return func(module string) string {
		if strings.HasPrefix(module, "stdlib://") {
			return ""
		}
		for _, d := range dirs {
			if strings.Contains(module, string(filepath.Separator)+d+string(filepath.Separator)) {
				return d
			}
		}
		return "app"
	}
}

func TestTraitDefaultChargesTheDeclaringPackage(t *testing.T) {
	for _, tc := range traitDefaultCases {
		t.Run(tc.capability, func(t *testing.T) {
			root := writeTree(t, map[string]string{
				"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nb = { path = \"../b\" }\n",
				"app/main.fern": `import "b";
struct R { n: i32 }
impl b.Leaky for R {
  function tag(self: Self): i32 { return self.n; }
}
function main(): i32 {
  let r: R = R { n: 1 };
  return r.grab();
}`,
				"b/fern.toml": "[package]\nname = \"b\"\n",
				"b/lib.fern": tc.imports + `pub trait Leaky {
  function tag(self: Self): i32;
  function grab(self: Self): i32 {
    ` + tc.stmt + `
    return 0;
  }
}`,
			})
			prog := loadChecked(t, filepath.Join(root, "app", "main.fern"))
			rows := caps.Analyze(prog, pkgByDir("b"))

			b := rowFor(t, rows, "b")
			if len(b.Uses) != 1 || b.Uses[0].Capability != tc.capability {
				t.Fatalf("declaring package must report %s alone, got %+v", tc.capability, b.Uses)
			}
			chain := b.Uses[0].Chain
			want := append(append([]string{}, tc.via...), tc.builtin)
			if len(chain) != 1+len(want) || !strings.Contains(chain[0], "grab") || strings.Join(chain[1:], " ") != strings.Join(want, " ") {
				t.Errorf("b's chain must be the synthesised default reaching %v on its own, got %v", want, chain)
			}

			errs, _ := caps.Enforce(rows, map[string]caps.Grant{
				"app": {Root: true},
				"b":   {Governed: true},
			})
			if len(errs) != 1 || errs[0].Package != "b" || errs[0].Capability != tc.capability {
				t.Fatalf("a governed declarer must be an E070 for %s, got %+v", tc.capability, errs)
			}
		})
	}
}

func TestTraitDefaultChargesTheDeclarerThroughAnIntermediate(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nmid = { path = \"../mid\" }\n",
		"app/main.fern": `import "mid";
function main(): i32 { return mid.run(); }`,
		"mid/fern.toml": "[package]\nname = \"mid\"\n[dependencies]\nb = { path = \"../b\" }\n",
		"mid/lib.fern": `import "b";
pub struct R { n: i32 }
impl b.Leaky for R {
  function tag(self: Self): i32 { return self.n; }
}
pub function run(): i32 {
  let r: R = R { n: 1 };
  return r.grab();
}`,
		"b/fern.toml": "[package]\nname = \"b\"\n",
		"b/lib.fern": `pub trait Leaky {
  function tag(self: Self): i32;
  function grab(self: Self): i32 {
    read_file("/etc/hostname");
    return 0;
  }
}`,
	})
	prog := loadChecked(t, filepath.Join(root, "app", "main.fern"))
	rows := caps.Analyze(prog, pkgByDir("b", "mid"))

	uses := rowFor(t, rows, "b").Uses
	if len(uses) != 1 || uses[0].Capability != "fs" {
		t.Fatalf("b declared the trait, so fs is b's, got %+v", uses)
	}
	if chain := uses[0].Chain; len(chain) != 2 || !strings.Contains(chain[0], "grab") || chain[1] != "read_file" {
		t.Errorf("b's chain must be the synthesised default reaching read_file, got %v", chain)
	}

	errs, _ := caps.Enforce(rows, map[string]caps.Grant{
		"app": {Root: true},
		"mid": {Governed: true, Caps: []string{"fs"}},
		"b":   {Governed: true},
	})
	if len(errs) != 1 || errs[0].Package != "b" || errs[0].Capability != "fs" {
		t.Fatalf("mid is granted fs; the declarer is not, so it alone violates. got %+v", errs)
	}
}

func rowFor(t *testing.T, rows []caps.Row, pkg string) caps.Row {
	t.Helper()
	for _, r := range rows {
		if r.Package == pkg {
			return r
		}
	}
	t.Fatalf("no row for package %q in %+v", pkg, rows)
	return caps.Row{}
}
