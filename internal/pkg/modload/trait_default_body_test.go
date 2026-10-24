package modload_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/ast"
)

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
function main(): i32 { let r: R = R { n: 1 }; return r.greet(); }`,
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
	for _, want := range []string{"lib__pub_helper", "lib__priv_helper"} {
		if !names[want] {
			t.Errorf("default body should call %q; names present: %v", want, names)
		}
	}
	for _, bad := range []string{"pub_helper", "priv_helper"} {
		if names[bad] {
			t.Errorf("default body still carries the bare name %q — the impl's module would capture it", bad)
		}
	}
}

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
function main(): i32 { let s: lib.S = lib.S { v: 1 }; return s.greet(); }`,
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

func TestTraitDefaultBodyEntryModuleKeepsBareNames(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `function h(): i32 { return 41; }
trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return h() + 1; }
}
struct R { n: i32 }
impl Greet for R { function tag(self: Self): i32 { return self.n; } }
function main(): i32 { let r: R = R { n: 1 }; return r.greet(); }`,
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
function main(): i32 { let r: R = R { n: 1 }; return r.greet(); }`,
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
