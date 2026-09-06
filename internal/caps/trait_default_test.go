package caps_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/caps"
)

// A trait's DEFAULT body is the trait module's source even though the
// clone is instantiated in each `impl`, so its capability use is charged
// to the package that DECLARED the trait (#8450). Charging it to the
// implementer laundered it: the ordinary way to adopt a trait is an
// `impl` supplying only the required methods, and when that impl is in
// the ROOT package — which is never enforced — the effectful default ran
// with no diagnostic at all.
//
// Analyze roots each package's walk in `FuncDecl.BodyModule()`, which
// prefers DefiningModule (the trait's module) over SourceModule (the
// impl's). These cases pin the enforcement end of that, one per v1
// capability, because the hole was invisible from the checker-side tests
// that only assert which module the body resolves in.

// traitDefaultCase is a v1 capability and a default-body statement that
// reaches it. Every body is shaped the same — discard the builtin's
// result, return 0 — so the only variable across the table is the
// builtin.
type traitDefaultCase struct {
	capability string
	builtin    string
	stmt       string
}

var traitDefaultCases = []traitDefaultCase{
	{"env", "env", `env("HOME");`},
	{"env", "hostname", `hostname();`},
	{"fs", "read_file", `read_file("/etc/hostname");`},
	{"net", "tcp_connect", `tcp_connect(1, 80);`},
	{"random", "random_i32", `random_i32();`},
	{"subprocess", "subprocess", `subprocess("/bin/true", [], "");`},
	{"time", "now_unix_ms", `now_unix_ms();`},
}

// pkgByDir maps a module path to the package whose directory contains
// it, folding the stdlib away.
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

// The root writes the `impl`; the capability must still land on the
// declaring package, and E070 must fire there once that package is
// governed. Before #8450 the row read "app", the root exemption
// swallowed it, and `fern -check` exited 0.
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
  var r: R = R { n: 1 };
  return r.grab();
}`,
				"b/fern.toml": "[package]\nname = \"b\"\n",
				"b/lib.fern": `pub trait Leaky {
  function tag(self: Self): i32;
  function grab(self: Self): i32 {
    ` + tc.stmt + `
    return 0;
  }
}`,
			})
			prog := loadChecked(t, filepath.Join(root, "app", "main.fern"))
			rows := caps.Analyze(prog, pkgByDir("b"))

			// The root reports it too, transitively — Analyze charges a
			// caller for what its callees reach. The regression is the
			// row BELOW: before #8450 the declaring package had none at
			// all, so the only report was the root's and the root is
			// exempt.
			b := rowFor(t, rows, "b")
			if len(b.Uses) != 1 || b.Uses[0].Capability != tc.capability {
				t.Fatalf("declaring package must report %s alone, got %+v", tc.capability, b.Uses)
			}
			chain := b.Uses[0].Chain
			if len(chain) != 2 || !strings.Contains(chain[0], "grab") || chain[1] != tc.builtin {
				t.Errorf("b's chain must be the synthesised default reaching %q on its own, got %v", tc.builtin, chain)
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

// The same laundering one hop away from the root: a middle package
// writes the impl. `mid` is enforceable, so this case never exited 0 —
// but the charge went to `mid` alone, and `mid` legitimately needs the
// grant for calling into `b`. Granting it therefore silenced the
// declarer's own use entirely, which is what the grant below sets up.
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
  var r: R = R { n: 1 };
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
