package modload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// writeFiles drops the supplied path → contents pairs into a fresh
// temp dir and returns the dir. Used by every test below to set up
// a tiny multi-file project on disk.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, contents := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func findFunc(prog *ast.Program, name string) *ast.FuncDecl {
	for _, fn := range prog.Funcs {
		if fn.Name == name {
			return fn
		}
	}
	return nil
}

func funcNames(prog *ast.Program) []string {
	out := []string{}
	for _, fn := range prog.Funcs {
		out = append(out, fn.Name)
	}
	return out
}

// callsDirect reports whether fn's body contains a direct call to
// `target` anywhere — used to assert the rewriter turned a
// `mod.fn` into a flat `mod__fn` reference.
func callsDirect(fn *ast.FuncDecl, target string) bool {
	found := false
	var walkExpr func(e ast.Expr)
	var walkStmt func(s ast.Stmt)
	walkExpr = func(e ast.Expr) {
		if e == nil || found {
			return
		}
		switch x := e.(type) {
		case *ast.Call:
			if id, ok := x.Callee.(*ast.Ident); ok && id.Name == target {
				found = true
				return
			}
			walkExpr(x.Callee)
			for _, a := range x.Args {
				walkExpr(a)
			}
		case *ast.Binary:
			walkExpr(x.Left)
			walkExpr(x.Right)
		case *ast.Unary:
			walkExpr(x.Operand)
		case *ast.Ternary:
			walkExpr(x.Cond)
			walkExpr(x.Then)
			walkExpr(x.Else)
		}
	}
	walkStmt = func(s ast.Stmt) {
		if s == nil || found {
			return
		}
		switch x := s.(type) {
		case *ast.Block:
			for _, c := range x.Stmts {
				walkStmt(c)
			}
		case *ast.Return:
			walkExpr(x.Value)
		case *ast.ExprStmt:
			walkExpr(x.Expr)
		case *ast.If:
			walkExpr(x.Cond)
			walkStmt(x.Then)
			walkStmt(x.Else)
		case *ast.While:
			walkExpr(x.Cond)
			walkStmt(x.Body)
		}
	}
	walkStmt(fn.Body)
	return found
}

// Two-file program: entry imports util and calls a function from it
// via the qualified-name syntax. After Load, the combined Program
// has both functions in one flat namespace, with util's function
// renamed to `util__greet` and the call rewritten to a direct
// reference.
func TestLoadCombinesEntryAndImport(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.lang": `function greet(): number { return 42; }`,
		"main.lang": `import "./util";
function main(): number { return util.greet(); }`,
	})
	prog, _, err := Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, fn := range prog.Funcs {
		names[fn.Name] = true
	}
	if !names["main"] {
		t.Errorf("entry's `main` should keep its original name; got %v", names)
	}
	if !names["util__greet"] {
		t.Errorf("util's `greet` should be mangled as `util__greet`; got %v", names)
	}
	main := findFunc(prog, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	if !callsDirect(main, "util__greet") {
		t.Errorf("main should call util__greet directly")
	}
}

// Same-module function values get prefixed too: a non-callee
// reference to an own-module function from a non-entry module is
// still a reference to the now-mangled name.
func TestLoadRenamesSameModuleFunctionValue(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.lang": `function add(a: number, b: number): number { return a + b; }
function pickAdd(): (number, number) => number { return add; }`,
		"main.lang": `import "./util";
function main(): number { return 0; }`,
	})
	prog, _, err := Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	pick := findFunc(prog, "util__pickAdd")
	if pick == nil {
		t.Fatalf("util__pickAdd not found in: %v", funcNames(prog))
	}
	ret, ok := pick.Body.Stmts[0].(*ast.Return)
	if !ok {
		t.Fatalf("expected first stmt of pickAdd to be Return, got %T", pick.Body.Stmts[0])
	}
	id, ok := ret.Value.(*ast.Ident)
	if !ok {
		t.Fatalf("expected return value to be Ident, got %T", ret.Value)
	}
	if id.Name != "util__add" {
		t.Errorf("function-value reference should be util__add, got %q", id.Name)
	}
}

// Imports are resolved relative to the importing file's directory,
// not the working directory or the entry file's directory. A nested
// import in a sibling subdirectory resolves through filepath.Join.
func TestLoadResolvesRelativeToImporter(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"helpers/inner.lang": `function answer(): number { return 7; }`,
		"helpers/util.lang": `import "./inner";
function call_inner(): number { return inner.answer(); }`,
		"main.lang": `import "./helpers/util";
function main(): number { return util.call_inner(); }`,
	})
	prog, _, err := Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	if findFunc(prog, "inner__answer") == nil {
		t.Errorf("expected inner__answer in combined program: %v", funcNames(prog))
	}
	if findFunc(prog, "util__call_inner") == nil {
		t.Errorf("expected util__call_inner in combined program: %v", funcNames(prog))
	}
}

// Cycles are detected and reported. A → B → A returns an error
// from Load rather than spinning forever or stack-overflowing.
func TestLoadDetectsCycle(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a.lang": `import "./b";
function fa(): number { return b.fb(); }`,
		"b.lang": `import "./a";
function fb(): number { return a.fa(); }`,
	})
	_, _, err := Load(filepath.Join(dir, "a.lang"))
	if err == nil {
		t.Fatal("expected cycle error from Load")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error should mention cycle; got %v", err)
	}
}

// Two imports binding to the same local name in the same file is a
// load-time error — qualified calls would be ambiguous.
func TestLoadRejectsDuplicateLocalName(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a/util.lang": `function fa(): number { return 1; }`,
		"b/util.lang": `function fb(): number { return 2; }`,
		"main.lang": `import "./a/util";
import "./b/util";
function main(): number { return util.fa(); }`,
	})
	_, _, err := Load(filepath.Join(dir, "main.lang"))
	if err == nil {
		t.Fatal("expected duplicate-import error from Load")
	}
}

// Single-file programs (no imports) still work — modload's load
// path is the only entry point now, so single-file behaviour has
// to round-trip cleanly.
func TestLoadSingleFileNoImports(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.lang": `function main(): number { return 99; }`,
	})
	prog, _, err := Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Funcs) != 1 || prog.Funcs[0].Name != "main" {
		t.Errorf("expected single `main` function, got %v", funcNames(prog))
	}
}

// Source map is populated with one entry per loaded module so
// diagnostics can find the right file for any error position.
func TestLoadReturnsPerFileSources(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.lang": `function f(): number { return 1; }`,
		"main.lang": `import "./util";
function main(): number { return util.f(); }`,
	})
	_, srcs, err := Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 2 {
		t.Errorf("expected 2 entries in srcs map (entry + util), got %d", len(srcs))
	}
}
