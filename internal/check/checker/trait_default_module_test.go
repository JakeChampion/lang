package checker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestTraitDefaultBodyReachesOwnModulePubHelper(t *testing.T) {
	err, _ := checkFiles(t, map[string]string{
		"lib.fern": `pub function secret_helper(): i32 { return 41; }
pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return secret_helper() + 1; }
}
`,
		"main.fern": `import "./lib";
struct R { n: i32 }
impl lib.Greet for R {
    function tag(self: Self): i32 { return self.n; }
}
function main(): i32 { let r: R = R { n: 1 }; return r.greet(); }
`,
	}, "main.fern")
	if err != nil {
		t.Errorf("a default body must reach its own module's pub helper:\n%v", err)
	}
}

func TestTraitDefaultBodyReachesOwnModulePrivateHelper(t *testing.T) {
	err, _ := checkFiles(t, map[string]string{
		"lib.fern": `function private_helper(): i32 { return 41; }
pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return private_helper() + 1; }
}
`,
		"main.fern": `import "./lib";
struct R { n: i32 }
impl lib.Greet for R {
    function tag(self: Self): i32 { return self.n; }
}
function main(): i32 { let r: R = R { n: 1 }; return r.greet(); }
`,
	}, "main.fern")
	if err != nil {
		t.Errorf("a default body must reach its own module's private helper:\n%v", err)
	}
}

func TestTraitDefaultBodyIsNotHijackedByTheImplementer(t *testing.T) {
	prog := loadFiles(t, map[string]string{
		"lib.fern": `pub function secret_helper(): i32 { return 41; }
pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return secret_helper() + 1; }
}
`,
		"main.fern": `import "./lib";
function secret_helper(): i32 { return 900; }
struct R { n: i32 }
impl lib.Greet for R {
    function tag(self: Self): i32 { return self.n; }
}
function main(): i32 { let r: R = R { n: 1 }; return r.greet(); }
`,
	}, "main.fern")
	if _, err := Check(prog); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	greet := synthesisedMethod(t, prog, "greet")
	names := bodyIdents(greet.Body)
	if !names["lib__secret_helper"] {
		t.Errorf("the materialised default must call the trait module's helper; body names: %v", names)
	}
	if names["secret_helper"] {
		t.Errorf("the materialised default calls the implementer's secret_helper — a library's default silently running consumer code; body names: %v", names)
	}
}

func TestTraitDefaultCarriesDefiningModule(t *testing.T) {
	prog := loadFiles(t, map[string]string{
		"lib.fern": `pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return self.tag() + 1; }
}
`,
		"main.fern": `import "./lib";
struct R { n: i32 }
impl lib.Greet for R {
    function tag(self: Self): i32 { return self.n; }
}
function main(): i32 { let r: R = R { n: 1 }; return r.greet(); }
`,
	}, "main.fern")
	if _, err := Check(prog); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	greet := synthesisedMethod(t, prog, "greet")
	if !strings.HasSuffix(greet.DefiningModule, "lib.fern") {
		t.Errorf("DefiningModule = %q, want the trait's module (lib.fern)", greet.DefiningModule)
	}
	if !strings.HasSuffix(greet.SourceModule, "main.fern") {
		t.Errorf("SourceModule = %q, want the impl's module (main.fern) — visibility and opaque access key on it", greet.SourceModule)
	}
	if greet.BodyModule() != greet.DefiningModule {
		t.Errorf("BodyModule() = %q, want DefiningModule %q", greet.BodyModule(), greet.DefiningModule)
	}
	if greet.P.Line != 3 {
		t.Errorf("synthesised method position = line %d, want the trait method's line 3", greet.P.Line)
	}
}

func TestTraitDefaultBodyReachesOwnModuleOpaqueFields(t *testing.T) {
	err, _ := checkFiles(t, map[string]string{
		"lib.fern": `pub opaque struct Email { addr: i32 }
pub function mk(): Email { return Email { addr: 41 }; }
pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { let e: Email = mk(); return e.addr + 1; }
}
`,
		"main.fern": `import "./lib";
struct R { n: i32 }
impl lib.Greet for R {
    function tag(self: Self): i32 { return self.n; }
}
function main(): i32 { let r: R = R { n: 1 }; return r.greet(); }
`,
	}, "main.fern")
	if err != nil {
		t.Errorf("a default body must reach its own module's opaque fields:\n%v", err)
	}
}

func TestTraitDefaultDiagnosticNamesTheTraitsSource(t *testing.T) {
	err, paths := checkFiles(t, map[string]string{
		"lib.fern": `pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return nowhere_at_all() + 1; }
}
`,
		"main.fern": `import "./lib";
struct R { n: i32 }
impl lib.Greet for R {
    function tag(self: Self): i32 { return self.n; }
}
function main(): i32 { let r: R = R { n: 1 }; return r.greet(); }
`,
	}, "main.fern")
	if err == nil {
		t.Fatal("an undefined identifier in a default body must still be reported")
	}
	if !strings.Contains(err.Error(), "nowhere_at_all") {
		t.Fatalf("expected E001 for nowhere_at_all, got: %v", err)
	}
	files := filesOf(t, err)
	for _, f := range files {
		if f != paths["lib.fern"] {
			t.Errorf("diagnostic attributed to %q, want the trait's file %q — the position is the trait's, so the file must be too", f, paths["lib.fern"])
		}
	}
}

func TestTraitDefaultBodyGenericAndChained(t *testing.T) {
	files := map[string]string{
		"lib.fern": `pub function base(): i32 { return 40; }
pub struct Wrap { w: i32 }
pub trait Conv[T] {
    function seed(self: Self): T;
    function one(self: Self): i32 { return base() + 1; }
    function two(self: Self): i32 { return self.one() + 1; }
    function boxed(self: Self): i32 { let b: Wrap = Wrap { w: base() }; return b.w; }
}
`,
		"main.fern": `import "./lib";
function base(): i32 { return 900; }
struct Wrap { w: i32 }
struct R { n: i32 }
impl lib.Conv[i32] for R {
    function seed(self: Self): i32 { return self.n; }
}
function main(): i32 { let r: R = R { n: 1 }; return r.one() + r.two() + r.boxed(); }
`,
	}
	if err, _ := checkFiles(t, files, "main.fern"); err != nil {
		t.Fatalf("generic trait / chained default / own-module struct literal must all resolve in the trait's module:\n%v", err)
	}
	prog := loadFiles(t, files, "main.fern")
	if _, err := Check(prog); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, tc := range []struct{ method, want string }{
		{"one", "lib__base"},
		{"boxed", "lib__base"},
	} {
		names := bodyIdents(synthesisedMethod(t, prog, tc.method).Body)
		if !names[tc.want] {
			t.Errorf("%s's default body should name %q; body names: %v", tc.method, tc.want, names)
		}
	}
	if lit := structLitNames(synthesisedMethod(t, prog, "boxed").Body); !lit["lib__Wrap"] {
		t.Errorf("boxed's struct literal should build lib__Wrap, got %v", lit)
	}
}

func structLitNames(b *ast.Block) map[string]bool {
	out := map[string]bool{}
	for _, s := range b.Stmts {
		ast.Walk(s, func(n ast.Node) bool {
			if sl, ok := n.(*ast.StructLit); ok {
				out[sl.TypeName] = true
			}
			return true
		})
	}
	return out
}

func loadFiles(t *testing.T, files map[string]string, entry string) *ast.Program {
	t.Helper()
	dir := t.TempDir()
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, entry))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return prog
}

func synthesisedMethod(t *testing.T, prog *ast.Program, name string) *ast.FuncDecl {
	t.Helper()
	for _, fn := range prog.Funcs {
		if fn.DefiningModule != "" && strings.HasSuffix(fn.Name, name) {
			return fn
		}
	}
	t.Fatalf("no materialised default named %q in %d funcs", name, len(prog.Funcs))
	return nil
}

func bodyIdents(b *ast.Block) map[string]bool {
	out := map[string]bool{}
	if b == nil {
		return out
	}
	for _, s := range b.Stmts {
		ast.Walk(s, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				out[id.Name] = true
			}
			return true
		})
	}
	return out
}
