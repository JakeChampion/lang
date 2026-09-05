package modload_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/modload"
)

// A trait's default body is mangled in the module that DECLARES the
// trait. The checker deep-clones that body into every impl, so a name
// left bare here would be resolved wherever the impl was written: the
// body could not reach its own module's helpers, and an implementing
// module declaring the same name would capture the call (#8484).

// identNames collects every Ident name reachable from a block — a call
// to `f()` is a Call whose Callee is an Ident — so a test can assert
// which form of a name survived the rewrite.
func identNames(b *ast.Block) map[string]bool {
	out := map[string]bool{}
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

// loadProg runs the full modload pipeline on an entry file.
func loadProg(t *testing.T, entry string) *ast.Program {
	t.Helper()
	prog, _, err := modload.Load(entry)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return prog
}

func findTrait(prog *ast.Program, suffix string) *ast.TraitDecl {
	for _, td := range prog.Traits {
		if strings.HasSuffix(td.Name, suffix) {
			return td
		}
	}
	return nil
}

func TestTraitDefaultBodyManglesInDeclaringModule(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"lib.fern": `pub function pub_helper(): i32 { return 41; }
function priv_helper(): i32 { return 1; }
pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return pub_helper() + priv_helper(); }
}`,
		"main.fern": `import "./lib";
function pub_helper(): i32 { return 900; }
struct R { n: i32 }
impl lib.Greet for R {
    function tag(self: Self): i32 { return self.n; }
}
function main(): i32 { var r: R = R { n: 1 }; return r.greet(); }`,
	})
	prog := loadProg(t, filepath.Join(dir, "main.fern"))
	td := findTrait(prog, "Greet")
	if td == nil {
		t.Fatalf("trait Greet not found in %v", prog.Traits)
	}
	var body *ast.Block
	for _, m := range td.Methods {
		if m.Name == "greet" {
			body = m.Body
		}
	}
	if body == nil {
		t.Fatal("greet has no default body")
	}
	names := identNames(body)
	// Both helpers — exported and private alike — resolve to lib's
	// mangled names. The private one is an intra-module reference, so
	// `pub` is irrelevant to the rewrite.
	for _, want := range []string{"lib__pub_helper", "lib__priv_helper"} {
		if !names[want] {
			t.Errorf("default body should call %q; names present: %v", want, names)
		}
	}
	// The bare forms would resolve in whichever module wrote the impl.
	for _, bad := range []string{"pub_helper", "priv_helper"} {
		if names[bad] {
			t.Errorf("default body still carries the bare name %q — the impl's module would capture it", bad)
		}
	}
}

// A trait implemented in the same module that declares it still works:
// the default body mangles to that module's own prefix, which is what
// the module's helpers were renamed to.
func TestTraitDefaultBodySameModuleImpl(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"lib.fern": `pub function h(): i32 { return 41; }
pub struct S { v: i32 }
pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return h() + 1; }
}
impl Greet for S { function tag(self: Self): i32 { return self.v; } }`,
		"main.fern": `import "./lib";
function main(): i32 { var s: lib.S = lib.S { v: 1 }; return s.greet(); }`,
	})
	prog := loadProg(t, filepath.Join(dir, "main.fern"))
	td := findTrait(prog, "Greet")
	if td == nil {
		t.Fatal("trait Greet not found")
	}
	for _, m := range td.Methods {
		if m.Name != "greet" {
			continue
		}
		if names := identNames(m.Body); !names["lib__h"] {
			t.Errorf("same-module default body should call lib__h; names present: %v", names)
		}
	}
}

// The entry module keeps its bare names (its mangle prefix is empty),
// so a trait declared there resolves against the flat program exactly
// as before.
func TestTraitDefaultBodyEntryModuleKeepsBareNames(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `function h(): i32 { return 41; }
trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return h() + 1; }
}
struct R { n: i32 }
impl Greet for R { function tag(self: Self): i32 { return self.n; } }
function main(): i32 { var r: R = R { n: 1 }; return r.greet(); }`,
	})
	prog := loadProg(t, filepath.Join(dir, "main.fern"))
	td := findTrait(prog, "Greet")
	if td == nil {
		t.Fatal("trait Greet not found")
	}
	for _, m := range td.Methods {
		if m.Name != "greet" {
			continue
		}
		if names := identNames(m.Body); !names["h"] {
			t.Errorf("entry-module default body should keep the bare name h; names present: %v", names)
		}
	}
}

// A qualified reference inside a default body (`other.f()`) rewrites to
// the imported module's mangled name, so the body reaches a module the
// implementer need never have imported.
func TestTraitDefaultBodyQualifiedImportRewrites(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"deep.fern": `pub function d(): i32 { return 41; }`,
		"lib.fern": `import "./deep";
pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return deep.d() + 1; }
}`,
		"main.fern": `import "./lib";
struct R { n: i32 }
impl lib.Greet for R { function tag(self: Self): i32 { return self.n; } }
function main(): i32 { var r: R = R { n: 1 }; return r.greet(); }`,
	})
	prog := loadProg(t, filepath.Join(dir, "main.fern"))
	td := findTrait(prog, "Greet")
	if td == nil {
		t.Fatal("trait Greet not found")
	}
	for _, m := range td.Methods {
		if m.Name != "greet" {
			continue
		}
		if names := identNames(m.Body); !names["deep__d"] {
			t.Errorf("default body should call deep__d; names present: %v", names)
		}
	}
}
