package modload_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
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
		case *ast.IfExpr:
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
		"util.lang": `pub function greet(): i32 { return 42; }`,
		"main.lang": `import "./util";
function main(): i32 { return util.greet(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
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
		"util.lang": `function add(a: i32, b: i32): i32 { return a + b; }
function pickAdd(): (i32, i32) => i32 { return add; }`,
		"main.lang": `import "./util";
function main(): i32 { return 0; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
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
		"helpers/inner.lang": `pub function answer(): i32 { return 7; }`,
		"helpers/util.lang": `import "./inner";
pub function call_inner(): i32 { return inner.answer(); }`,
		"main.lang": `import "./helpers/util";
function main(): i32 { return util.call_inner(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
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
pub function fa(): i32 { return b.fb(); }`,
		"b.lang": `import "./a";
pub function fb(): i32 { return a.fa(); }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "a.lang"))
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
		"a/util.lang": `pub function fa(): i32 { return 1; }`,
		"b/util.lang": `pub function fb(): i32 { return 2; }`,
		"main.lang": `import "./a/util";
import "./b/util";
function main(): i32 { return util.fa(); }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err == nil {
		t.Fatal("expected duplicate-import error from Load")
	}
}

// Single-file programs (no imports) still work — modload's load
// path is the only entry point now, so single-file behaviour has
// to round-trip cleanly.
func TestLoadSingleFileNoImports(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.lang": `function main(): i32 { return 99; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Funcs) != 1 || prog.Funcs[0].Name != "main" {
		t.Errorf("expected single `main` function, got %v", funcNames(prog))
	}
}

// Cross-module struct types: an entry that says `var p: mod.Foo`
// must rewrite the type annotation to the mangled name. The struct
// declaration in the imported module gets prefixed; the entry's
// reference to it gets flattened.
func TestLoadRewritesCrossModuleStructType(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"point.lang": `pub struct Point { x: i32, y: i32 }
pub function make(x: i32, y: i32): Point {
	return Point { x: x, y: y };
}`,
		"main.lang": `import "./point";
function main(): i32 {
	var p: point.Point = point.make(3, 4);
	return p.x + p.y;
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	// Imported struct must show up under its mangled name.
	hasPointStruct := false
	for _, sd := range prog.Structs {
		if sd.Name == "point__Point" {
			hasPointStruct = true
		}
	}
	if !hasPointStruct {
		t.Errorf("expected struct point__Point in combined program; got %v", prog.Structs)
	}
	// Entry's `var p: point.Point` must have been rewritten to
	// reference the mangled type.
	main := findFunc(prog, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	v, ok := main.Body.Stmts[0].(*ast.Var)
	if !ok {
		t.Fatalf("expected first stmt to be Var, got %T", main.Body.Stmts[0])
	}
	st, ok := v.Type.(ast.StructType)
	if !ok {
		t.Fatalf("expected Var.Type to be StructType, got %T", v.Type)
	}
	if st.Name != "point__Point" {
		t.Errorf("Var.Type should reference point__Point, got %q", st.Name)
	}
}

// Cross-module struct LITERALS: `mod.Foo { x: 1, y: 2 }` parses
// as a StructLit with TypeName "mod.Foo" and modload should
// flatten it to the mangled form before the checker sees it.
func TestLoadRewritesCrossModuleStructLit(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"point.lang": `pub struct Point { x: i32, y: i32 }`,
		"main.lang": `import "./point";
function main(): i32 {
	var p: point.Point = point.Point { x: 5, y: 7 };
	return p.x;
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	main := findFunc(prog, "main")
	v := main.Body.Stmts[0].(*ast.Var)
	lit, ok := v.Init.(*ast.StructLit)
	if !ok {
		t.Fatalf("expected Var.Init to be StructLit, got %T", v.Init)
	}
	if lit.TypeName != "point__Point" {
		t.Errorf("StructLit.TypeName should be point__Point, got %q", lit.TypeName)
	}
}

// Cross-module struct in a function-return type: `function f(): mod.Foo`
// works the same way — modload's rewriteType walks every type
// position the parser might emit.
func TestLoadRewritesCrossModuleReturnType(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"point.lang": `pub struct Point { x: i32, y: i32 }
pub function origin(): Point { return Point { x: 0, y: 0 }; }`,
		"main.lang": `import "./point";
function pickOrigin(): point.Point { return point.origin(); }
function main(): i32 { return pickOrigin().x; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	pick := findFunc(prog, "pickOrigin")
	if pick == nil {
		t.Fatal("pickOrigin not found")
	}
	st, ok := pick.ReturnType.(ast.StructType)
	if !ok {
		t.Fatalf("expected return type StructType, got %T", pick.ReturnType)
	}
	if st.Name != "point__Point" {
		t.Errorf("return type should be point__Point, got %q", st.Name)
	}
}

// Cross-module references to a non-`pub` function are a load-time
// error. The diagnostic mentions the offending qualified name and
// hints at the fix.
func TestLoadRejectsPrivateFunctionAccess(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.lang": `function secret(): i32 { return 9; }`,
		"main.lang": `import "./util";
function main(): i32 { return util.secret(); }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err == nil {
		t.Fatal("expected visibility error from Load")
	}
	if !strings.Contains(err.Error(), "util.secret") || !strings.Contains(err.Error(), "not exported") {
		t.Errorf("error should mention `util.secret` and `not exported`; got %v", err)
	}
}

// Cross-module function-value references (taking a private function
// as a value, not calling it) are equally rejected.
func TestLoadRejectsPrivateFunctionValueReference(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.lang": `function secret(): i32 { return 9; }`,
		"main.lang": `import "./util";
function main(): i32 {
	var f: () => i32 = util.secret;
	return f();
}`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err == nil {
		t.Fatal("expected visibility error from Load")
	}
	if !strings.Contains(err.Error(), "util.secret") {
		t.Errorf("error should mention `util.secret`; got %v", err)
	}
}

// Cross-module struct-type references to a non-`pub` struct are
// rejected. The fix-hint mentions `pub struct`.
func TestLoadRejectsPrivateStructType(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"point.lang": `struct Point { x: i32, y: i32 }`,
		"main.lang": `import "./point";
function main(): i32 {
	var p: point.Point = point.Point { x: 1, y: 2 };
	return p.x;
}`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err == nil {
		t.Fatal("expected visibility error from Load")
	}
	if !strings.Contains(err.Error(), "point.Point") || !strings.Contains(err.Error(), "pub struct") {
		t.Errorf("error should mention `point.Point` and the `pub struct` hint; got %v", err)
	}
}

// Private decls remain freely callable INSIDE their own module —
// visibility only gates cross-module access. A non-`pub` helper
// referenced from a `pub` function in the same file loads cleanly.
func TestLoadAllowsPrivateAccessWithinSameModule(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.lang": `function helper(): i32 { return 1; }
pub function exposed(): i32 { return helper() + 1; }`,
		"main.lang": `import "./util";
function main(): i32 { return util.exposed(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatalf("private same-module access should be allowed: %v", err)
	}
	if findFunc(prog, "util__helper") == nil {
		t.Errorf("private helper should still appear in the combined program; got %v", funcNames(prog))
	}
}

// Cross-module references to a `pub const` flow through the
// rewriter the same way a `pub function` does — the local-name
// `mod.K` is flattened to `mod__K` and a same-module reference
// to a private const stays bound at its mangled name. The const
// decl shows up in the combined program with the mangled name; the
// constfold pass (run by the driver, not by Load) is responsible
// for resolving references afterwards.
func TestLoadCombinesPubConstAcrossModules(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"limits.lang": `pub const MAX: i32 = 100;`,
		"main.lang": `import "./limits";
function main(): i32 { return limits.MAX; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	hasMangled := false
	for _, cd := range prog.Consts {
		if cd.Name == "limits__MAX" {
			hasMangled = true
		}
	}
	if !hasMangled {
		t.Errorf("expected mangled const limits__MAX in combined program; got %v", prog.Consts)
	}
	main := findFunc(prog, "main")
	ret, ok := main.Body.Stmts[0].(*ast.Return)
	if !ok {
		t.Fatalf("expected first stmt to be Return, got %T", main.Body.Stmts[0])
	}
	id, ok := ret.Value.(*ast.Ident)
	if !ok {
		t.Fatalf("expected return value to be Ident, got %T", ret.Value)
	}
	if id.Name != "limits__MAX" {
		t.Errorf("expected reference to limits__MAX, got %q", id.Name)
	}
}

// Cross-module references to a non-`pub` const are rejected with a
// fix-hint that names the right keyword (`pub const`, not
// `pub function`) — the modload error path inspects the imported
// module's decls so the diagnostic matches the actual decl kind.
func TestLoadRejectsPrivateConstAccess(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"limits.lang": `const MAX: i32 = 100;`,
		"main.lang": `import "./limits";
function main(): i32 { return limits.MAX; }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err == nil {
		t.Fatal("expected visibility error from Load")
	}
	if !strings.Contains(err.Error(), "limits.MAX") {
		t.Errorf("error should mention `limits.MAX`; got %v", err)
	}
	if !strings.Contains(err.Error(), "pub const") {
		t.Errorf("error should suggest `pub const`; got %v", err)
	}
}

// Source map is populated with one entry per loaded module so
// diagnostics can find the right file for any error position.
func TestLoadReturnsPerFileSources(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.lang": `pub function f(): i32 { return 1; }`,
		"main.lang": `import "./util";
function main(): i32 { return util.f(); }`,
	})
	_, srcs, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 2 {
		t.Errorf("expected 2 entries in srcs map (entry + util), got %d", len(srcs))
	}
}

// Stdlib resolver: `import "std/…";` routes through the embedded
// internal/stdlib FS rather than the local filesystem. Phase 1
// of the prelude-to-modules migration (see
// docs/PRELUDE-TO-MODULES.md) — this test pins the wiring before
// real stdlib modules land.
func TestLoadResolvesStdlibImport(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.lang": `import "std/_test_empty";
function main(): i32 { return _test_empty.stdlib_test_marker(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	// After flattening, the std module's function lands at the
	// mangled name `_test_empty__stdlib_test_marker`. The entry
	// module's `_test_empty.stdlib_test_marker()` call should be
	// rewritten to a direct call to that name.
	if findFunc(prog, "_test_empty__stdlib_test_marker") == nil {
		t.Errorf("expected mangled stdlib function in combined program; got %v", funcNames(prog))
	}
}

// Mirror of the std/ case for the core/ prefix — both share a
// single Resolve code path in the stdlib package, but the test
// proves the prefix-classifier in modload accepts both.
func TestLoadResolvesCoreImport(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.lang": `import "core/_test_empty";
function main(): i32 { return _test_empty.core_test_marker(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	if findFunc(prog, "_test_empty__core_test_marker") == nil {
		t.Errorf("expected mangled core function in combined program; got %v", funcNames(prog))
	}
}

// Unknown stdlib module → a clear error rather than a fallthrough
// to filesystem resolution (which would produce a confusing
// "read stdlib://…: no such file" message from os.ReadFile).
func TestLoadUnknownStdlibModule(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.lang": `import "std/does_not_exist";
function main(): i32 { return 0; }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err == nil {
		t.Fatal("expected error for unknown stdlib module")
	}
	if !strings.Contains(err.Error(), "unknown stdlib module") {
		t.Errorf("expected `unknown stdlib module` in error; got %v", err)
	}
}

// Every FuncDecl gets stamped with the path of the module that
// declared it. Cross-module method dispatch (Phase 3 of the
// prelude-to-modules migration) relies on this stamp to scope
// method visibility — methods declared in module A are only
// callable from files whose import closure reaches A.
func TestLoadStampsFuncDeclSourceModule(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.lang": `pub function f(): i32 { return 1; }`,
		"main.lang": `import "./util";
function main(): i32 { return util.f(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	mainAbs, _ := filepath.Abs(filepath.Join(dir, "main.lang"))
	utilAbs, _ := filepath.Abs(filepath.Join(dir, "util.lang"))
	got := map[string]string{}
	for _, fn := range prog.Funcs {
		got[fn.Name] = fn.SourceModule
	}
	if got["main"] != mainAbs {
		t.Errorf("main.SourceModule = %q, want %q", got["main"], mainAbs)
	}
	if got["util__f"] != utilAbs {
		t.Errorf("util__f.SourceModule = %q, want %q", got["util__f"], utilAbs)
	}
}

// Transitive import closures: a module's closure includes
// itself plus every module reachable by an import-chain. The
// checker reads this to answer the "is the receiver's source
// module visible from here?" question during method dispatch.
func TestLoadComputesImportClosures(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"c.lang": `pub function c_fn(): i32 { return 3; }`,
		"b.lang": `import "./c";
pub function b_fn(): i32 { return c.c_fn() + 2; }`,
		"a.lang": `import "./b";
function main(): i32 { return b.b_fn() + 1; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "a.lang"))
	if err != nil {
		t.Fatal(err)
	}
	aAbs, _ := filepath.Abs(filepath.Join(dir, "a.lang"))
	bAbs, _ := filepath.Abs(filepath.Join(dir, "b.lang"))
	cAbs, _ := filepath.Abs(filepath.Join(dir, "c.lang"))

	if prog.ModuleImports == nil {
		t.Fatal("ModuleImports map is nil")
	}
	if !prog.ModuleImports[aAbs][aAbs] {
		t.Errorf("closure[a] should contain a itself")
	}
	if !prog.ModuleImports[aAbs][bAbs] {
		t.Errorf("closure[a] should contain b (direct import)")
	}
	if !prog.ModuleImports[aAbs][cAbs] {
		t.Errorf("closure[a] should contain c (transitive via b)")
	}

	// Reverse direction shouldn't leak: c doesn't import a or b.
	if prog.ModuleImports[cAbs][bAbs] {
		t.Errorf("closure[c] should NOT contain b")
	}
	if prog.ModuleImports[cAbs][aAbs] {
		t.Errorf("closure[c] should NOT contain a")
	}
	// Self-membership is universal.
	if !prog.ModuleImports[cAbs][cAbs] {
		t.Errorf("closure[c] should contain c itself")
	}
}

// Module-scoped method dispatch (Phase 3 of the prelude-to-
// modules migration): a method declared in module L is callable
// from module M only when M's import closure reaches L.
//
// Importer sees the method:
func TestMethodVisibleAcrossExplicitImport(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"lib.lang":  `pub function (n: i32) my_method(): i32 { return n + 1; }`,
		"main.lang": `import "./lib";
function main(): i32 { return (5).my_method(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("expected method to resolve, got %v", err)
	}
}

// Non-importer can't see the method — the receiver-typed call
// shape goes unresolved and the checker complains. The exact
// diagnostic could improve (today it surfaces as a generic
// "unresolved" rather than naming the missing import), but the
// PRESENCE of a non-nil error is the load-bearing behaviour:
// silently accepting the call would defeat module scoping.
func TestMethodNotVisibleWithoutImport(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"lib.lang":  `pub function (n: i32) my_method(): i32 { return n + 1; }`,
		"main.lang": `function main(): i32 { return (5).my_method(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatal("expected checker error when main doesn't import lib")
	}
}

// Transitive: A imports B, B imports C, C defines a method. A
// should see the method via the closure (B's import chain pulls
// C in).
func TestMethodVisibleViaTransitiveImport(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"c.lang": `pub function (n: i32) deep(): i32 { return n * 3; }`,
		"b.lang": `import "./c";
pub function passthrough(n: i32): i32 { return n.deep(); }`,
		"a.lang": `import "./b";
function main(): i32 { return (4).deep(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "a.lang"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("expected transitive visibility, got %v", err)
	}
}

// User code can `import "std/i32";` to bring the migrated
// methods into scope. The auto-prelude also imports the same
// module, but `prog.LoadedStdlibPaths` dedupes so the receiver-
// method redeclaration check doesn't fire.
func TestUserImportOfStdI32CoexistsWithAutoPrelude(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.lang": `import "std/i32";
function main(): i32 { return (0 - 9).abs(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("expected user-import + prelude dedup, got %v", err)
	}
}

// `import "core/no_prelude";` opts a program out of the auto-
// injected magic prelude. The user program then needs to
// `import` every stdlib module it uses explicitly. Phase 5 of
// the prelude-to-modules migration relies on this gate to land
// without a single mega-PR.
//
// Positive case: a no-prelude program that doesn't need any
// stdlib at all type-checks cleanly. Proves the opt-out
// doesn't accidentally break otherwise-valid programs.
//
// Programs that DO need stdlib helpers under no-prelude need
// `import "std/foo";` for each module they touch, AND each
// std/* module's internal cross-module refs need to be
// qualified (e.g. `int.int_to_string_radix(…)`) since modload
// mangles non-receiver names on import. The current std/* sources
// rely on the auto-prelude flattening their decls into one
// namespace; cleaning that up for the no-prelude path is a
// follow-up — until then, no-prelude programs that lean on
// the stdlib will hit unresolved-name errors.
func TestNoPreludeBareProgramTypechecks(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.lang": `import "core/no_prelude";
function main(): i32 { return 42; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("expected no-prelude bare program to compile, got %v", err)
	}
}

// Negative case: program imports core/no_prelude but doesn't
// import std/i32. The `(5).abs()` call has no `abs` method in
// scope and the checker errors. Without the opt-out the auto-
// prelude would silently supply the method; with the opt-out
// the missing import is caught.
func TestNoPreludeMissingImportErrors(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.lang": `import "core/no_prelude";
function main(): i32 { return (5).abs(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.lang"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatal("expected checker error when no-prelude is set and std/i32 isn't imported")
	}
}

// LoadStdlibFlat loads stdlib modules under a flat namespace —
// decls keep their bare names, and qualified call sites inside
// stdlib bodies (`int.foo()`) rewrite to bare calls (`foo()`).
// The result is the shape the auto-prelude path needs: every
// stdlib decl callable by name from user code, plus the
// freedom for stdlib internals to use `import "core/int";` +
// `int.foo(...)` style qualified calls.
func TestLoadStdlibFlatRewritesQualifiedToBare(t *testing.T) {
	prog, err := modload.LoadStdlibFlat([]string{"std/u32"})
	if err != nil {
		t.Fatalf("LoadStdlibFlat: %v", err)
	}
	// std/u32 has an `import "core/int"` and calls
	// `int.__int_to_string_u64(...)`. Find the `(n: u32)
	// to_string()` receiver method and verify its body calls
	// bare `__int_to_string_u64`, not the mangled `int__...`
	// form.
	var fn *ast.FuncDecl
	for _, f := range prog.Funcs {
		if f.Receiver == nil || f.Name != "to_string" {
			continue
		}
		nt, ok := f.Receiver.Type.(ast.NumberType)
		if !ok || nt.Width != 32 || nt.Signed {
			continue
		}
		fn = f
		break
	}
	if fn == nil {
		t.Fatalf("did not find (u32).to_string in: %v", funcNames(prog))
	}
	if !callsDirect(fn, "__int_to_string_u64") {
		t.Errorf("(u32).to_string body should call bare __int_to_string_u64; flat rewriter did not strip the qualifier")
	}
	if callsDirect(fn, "int__int_to_string_u64") {
		t.Errorf("(u32).to_string body should NOT call mangled int__int_to_string_u64 under LoadStdlibFlat")
	}
}

// Core/int is pulled in transitively when LoadStdlibFlat
// loads std/u32 (which imports it). Its decls land bare-named
// — confirms the flat-namespace mode applies to transitive
// loads, not just the top-level paths the caller asked for.
func TestLoadStdlibFlatPullsTransitiveImports(t *testing.T) {
	prog, err := modload.LoadStdlibFlat([]string{"std/u32"})
	if err != nil {
		t.Fatalf("LoadStdlibFlat: %v", err)
	}
	if findFunc(prog, "__int_to_string_u64") == nil {
		t.Errorf("core/int's __int_to_string_u64 should land at the bare name when pulled in transitively: %v",
			funcNames(prog))
	}
	if findFunc(prog, "int__int_to_string_u64") != nil {
		t.Errorf("core/int's helpers should NOT be mangled under LoadStdlibFlat")
	}
}

// Non-stdlib paths are rejected upfront so a misuse surfaces
// a clean error instead of the loader's "file not found".
func TestLoadStdlibFlatRejectsNonStdlibPath(t *testing.T) {
	if _, err := modload.LoadStdlibFlat([]string{"./relative/util"}); err == nil {
		t.Fatal("expected LoadStdlibFlat to reject non-stdlib path")
	}
}
