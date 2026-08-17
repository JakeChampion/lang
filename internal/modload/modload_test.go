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
		"util.fern": `pub function greet(): i32 { return 42; }`,
		"main.fern": `import "./util";
function main(): i32 { return util.greet(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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

// An import alias binds the qualifier to the `as` name; modload keys
// its per-module import table off Import.LocalName (= the alias), so
// `u.greet()` resolves to the imported module exactly like the
// basename qualifier would.
func TestLoadResolvesImportAlias(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.fern": `pub function greet(): i32 { return 42; }`,
		"main.fern": `import "./util" as u;
function main(): i32 { return u.greet(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	main := findFunc(prog, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	if !callsDirect(main, "util__greet") {
		t.Errorf("aliased `u.greet()` should rewrite to util__greet; funcs: %v", funcNames(prog))
	}
}

// Two imports bound to the same qualifier (whether by alias or
// collision with a basename) are a load-time error — the qualifier
// would be ambiguous.
func TestLoadRejectsDuplicateAlias(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a.fern": `pub function f(): i32 { return 1; }`,
		"b.fern": `pub function g(): i32 { return 2; }`,
		"main.fern": `import "./a" as x;
import "./b" as x;
function main(): i32 { return x.f(); }`,
	})
	if _, _, err := modload.Load(filepath.Join(dir, "main.fern")); err == nil {
		t.Fatal("expected a duplicate-qualifier error for two `as x` imports")
	}
}

// Two distinct modules sharing a basename (a/util.fern + b/util.fern),
// imported under distinct aliases, must each get a UNIQUE mangle prefix
// so their decls don't both become `util__val` and trip a spurious
// "redeclared" error. Regression for M2 in
// docs/ADVERSARIAL-REVIEW-2026-06.md.
func TestLoadDisambiguatesSameBasenameModules(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a/util.fern": `pub function val(): i32 { return 1; }`,
		"b/util.fern": `pub function val(): i32 { return 2; }`,
		"main.fern": `import "./a/util" as au;
import "./b/util" as bu;
function main(): i32 { return au.val() * 10 + bu.val(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatalf("same-basename modules under distinct aliases should load: %v", err)
	}
	// Both val() decls survive under distinct mangled names, and main's
	// two call sites resolve to those distinct names.
	var valCount int
	for _, n := range funcNames(prog) {
		if strings.HasSuffix(n, "__val") {
			valCount++
		}
	}
	if valCount != 2 {
		t.Errorf("expected 2 distinct mangled val() decls, got %d in %v", valCount, funcNames(prog))
	}
	main := findFunc(prog, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	if !callsDirect(main, "util__val") || !callsDirect(main, "util_1__val") {
		t.Errorf("main should call both util__val and util_1__val; funcs: %v", funcNames(prog))
	}
}

// Same-module function values get prefixed too: a non-callee
// reference to an own-module function from a non-entry module is
// still a reference to the now-mangled name.
func TestLoadRenamesSameModuleFunctionValue(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.fern": `function add(a: i32, b: i32): i32 { return a + b; }
function pickAdd(): (i32, i32) => i32 { return add; }`,
		"main.fern": `import "./util";
function main(): i32 { return 0; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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

// A struct-update literal nested inside a tuple-literal return value
// must have its TypeName mangled with the module prefix, and its
// spread Base must be rewritten too. This is the cursor idiom's shape
// (`return (result, Cur { ...p, pos: … })`) — a rewriter that never
// descends into TupleLit elements leaves the StructLit TypeName bare and
// the checker reports "unknown struct type". Regress
// against that: walk the mangled function body and assert no bare
// `Cur` StructLit survives.
func TestLoadMangleStructUpdateInTupleReturn(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"cur.fern": `pub struct Cur { s: string, pos: i32 }
pub function adv(p: Cur): (i32, Cur) {
    return (p.pos, Cur { ...p, pos: p.pos + 1 });
}`,
		"main.fern": `import "./cur";
function main(): i32 { return 0; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	adv := findFunc(prog, "cur__adv")
	if adv == nil {
		t.Fatalf("cur__adv not found in: %v", funcNames(prog))
	}
	var bare int
	var mangled int
	ast.Walk(adv.Body, func(n ast.Node) bool {
		sl, ok := n.(*ast.StructLit)
		if !ok {
			return true
		}
		switch sl.TypeName {
		case "Cur":
			bare++
		case "cur__Cur":
			mangled++
		}
		return true
	})
	if bare != 0 {
		t.Errorf("struct-update inside tuple return left %d bare \"Cur\" StructLit(s); want all mangled", bare)
	}
	if mangled != 1 {
		t.Errorf("expected exactly 1 mangled \"cur__Cur\" StructLit, got %d", mangled)
	}
}

// Imports are resolved relative to the importing file's directory,
// not the working directory or the entry file's directory. A nested
// import in a sibling subdirectory resolves through filepath.Join.
func TestLoadResolvesRelativeToImporter(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"helpers/inner.fern": `pub function answer(): i32 { return 7; }`,
		"helpers/util.fern": `import "./inner";
pub function call_inner(): i32 { return inner.answer(); }`,
		"main.fern": `import "./helpers/util";
function main(): i32 { return util.call_inner(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"a.fern": `import "./b";
pub function fa(): i32 { return b.fb(); }`,
		"b.fern": `import "./a";
pub function fb(): i32 { return a.fa(); }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "a.fern"))
	if err == nil {
		t.Fatal("expected cycle error from Load")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error should mention cycle; got %v", err)
	}
}

// `LoadStdlibFlatSkipping` excludes the named paths from the
// combined Program (so a user program that already loaded those
// paths through modload's mangling path doesn't end up with two
// copies of every `__method_<Type>_<Name>` decl), AND rewrites
// references TO skipped paths from non-skipped modules using the
// modload `<mod>__` prefix instead of bare. Without the latter,
// a flat-namespace body referencing `int.foo()` qualified would
// rewrite to bare `foo()` — but the entry program's modload
// load of core/int has the decl under the mangled name only.
func TestLoadStdlibFlatSkippingRewritesToMangledForSkippedPaths(t *testing.T) {
	skip := map[string]bool{
		"stdlib://core/int.fern": true,
	}
	prog, err := modload.LoadStdlibFlatSkipping([]string{"std/u32"}, skip)
	if err != nil {
		t.Fatalf("LoadStdlibFlatSkipping(std/u32, skip core/int): %v", err)
	}
	// std/u32 (u32).to_string body should now call the mangled
	// form `int____int_to_string_u64`, not the bare flat form.
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
		t.Fatalf("did not find (u32).to_string: %v", funcNames(prog))
	}
	if !callsDirect(fn, "int____int_to_string_u64") {
		t.Errorf("(u32).to_string body should call mangled int____int_to_string_u64 when core/int is skipped; the bare flat form leaves the reference dangling against the entry's modload-mangled copy")
	}
	// And core/int's own decls should be ABSENT from the
	// combined Program (the entry's modload load owns them).
	if findFunc(prog, "__int_to_string_u64") != nil {
		t.Errorf("LoadStdlibFlatSkipping should not include skipped path's decls in combined Program")
	}
}

// Cycles between two STDLIB modules are allowed — the stdlib's
// method graph has natural cycles (std/string ↔ std/i32 for byte
// methods both ways) that modload's regular cycle gate would
// reject. The back-edge's imports[localName] pointer is patched
// up in a second pass once both modules are in `loaded`.
//
// The `_test_empty` fixtures import each other to form the
// canonical cycle (std/_test_empty ↔ core/_test_empty). Before
// the cycle gate this load would error with
// `import cycle detected including stdlib://…`.
func TestLoadStdlibFlatAllowsCycles(t *testing.T) {
	prog, err := modload.LoadStdlibFlat([]string{"std/_test_empty"})
	if err != nil {
		t.Fatalf("LoadStdlibFlat(std/_test_empty) failed under stdlib cycle: %v", err)
	}
	// Both ends of the cycle should be in the combined Program.
	if findFunc(prog, "stdlib_test_marker") == nil {
		t.Errorf("expected stdlib_test_marker in combined Program: %v", funcNames(prog))
	}
	if findFunc(prog, "core_test_marker") == nil {
		t.Errorf("expected core_test_marker pulled in via cycle: %v", funcNames(prog))
	}
}

// Same cycle through the regular Load path (which mangles
// non-entry decls). Confirms the gate isn't LoadStdlibFlat-
// specific — any user program that imports a cyclically-
// referencing pair of stdlib modules goes through the same
// codepath.
func TestLoadAllowsStdlibCyclesViaUserImport(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `
import "std/_test_empty";
function main(): i32 { return _test_empty.stdlib_test_marker(); }`,
	})
	if _, _, err := modload.Load(filepath.Join(dir, "main.fern")); err != nil {
		t.Fatalf("Load of program importing cyclic stdlib failed: %v", err)
	}
}

// Two imports binding to the same local name in the same file is a
// load-time error — qualified calls would be ambiguous.
func TestLoadRejectsDuplicateLocalName(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a/util.fern": `pub function fa(): i32 { return 1; }`,
		"b/util.fern": `pub function fb(): i32 { return 2; }`,
		"main.fern": `import "./a/util";
import "./b/util";
function main(): i32 { return util.fa(); }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err == nil {
		t.Fatal("expected duplicate-import error from Load")
	}
}

// Single-file programs (no imports) still work — modload's load
// path is the only entry point now, so single-file behaviour has
// to round-trip cleanly.
func TestLoadSingleFileNoImports(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `function main(): i32 { return 99; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"point.fern": `pub struct Point { x: i32, y: i32 }
pub function make(x: i32, y: i32): Point {
	return Point { x: x, y: y };
}`,
		"main.fern": `import "./point";
function main(): i32 {
	var p: point.Point = point.make(3, 4);
	return p.x + p.y;
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"point.fern": `pub struct Point { x: i32, y: i32 }`,
		"main.fern": `import "./point";
function main(): i32 {
	var p: point.Point = point.Point { x: 5, y: 7 };
	return p.x;
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"point.fern": `pub struct Point { x: i32, y: i32 }
pub function origin(): Point { return Point { x: 0, y: 0 }; }`,
		"main.fern": `import "./point";
function pickOrigin(): point.Point { return point.origin(); }
function main(): i32 { return pickOrigin().x; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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

// Module-local struct names inside compound type positions — tuples
// and generic type-args — must get the `<mod>__` prefix too. Regression
// guard for the flip-era bug where `function f(): (string, Local)`
// left `Local` un-mangled (the return-type rewriter only recursed into
// arrays + func types), so the tuple element didn't match the mangled
// struct decl and the checker rejected the program.
func TestLoadRewritesStructInsideCompoundTypes(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"box.fern": `pub struct Box { v: i32 }
pub function pair(): (i32, Box) { return (0, Box { v: 1 }); }
pub function maybe(): Option[Box] { return Some(Box { v: 2 }); }
pub function many(): Box[] { return [Box { v: 3 }]; }`,
		"main.fern": `import "./box";
function main(): i32 {
    var t = box.pair();
    var arr = box.many();
    match (box.maybe()) {
        Some(b) => { return t.1.v + arr[0].v + b.v; },
        None => { return 0; }
    }
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	// Tuple element.
	pair := findFunc(prog, "box__pair")
	if pair == nil {
		t.Fatal("box__pair not found")
	}
	tt, ok := pair.ReturnType.(ast.TupleType)
	if !ok || len(tt.Elems) != 2 {
		t.Fatalf("pair return type = %T, want 2-tuple", pair.ReturnType)
	}
	if st, ok := tt.Elems[1].(ast.StructType); !ok || st.Name != "box__Box" {
		t.Errorf("tuple element 1 = %v, want StructType box__Box", tt.Elems[1])
	}
	// Generic type-arg (Option[Box]).
	maybe := findFunc(prog, "box__maybe")
	if et, ok := maybe.ReturnType.(ast.EnumType); !ok || len(et.Args) != 1 {
		t.Fatalf("maybe return type = %T, want Option[...]", maybe.ReturnType)
	} else if st, ok := et.Args[0].(ast.StructType); !ok || st.Name != "box__Box" {
		t.Errorf("Option arg = %v, want box__Box", et.Args[0])
	}
	// Array element.
	many := findFunc(prog, "box__many")
	if at, ok := many.ReturnType.(ast.ArrayType); !ok {
		t.Fatalf("many return type = %T, want array", many.ReturnType)
	} else if st, ok := at.Elem.(ast.StructType); !ok || st.Name != "box__Box" {
		t.Errorf("array elem = %v, want box__Box", at.Elem)
	}
}

// Cross-module references to a non-`pub` function are a load-time
// error. The diagnostic mentions the offending qualified name and
// hints at the fix.
// `pub(package)` decls are visible to a module in the SAME package (same
// directory) — `util.helper()` resolves like a `pub` decl would. See
// docs/PUB-PACKAGE.md.
func TestLoadPubPackageSamePackage(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.fern": `pub(package) function helper(): i32 { return 42; }`,
		"main.fern": `import "./util";
function main(): i32 { return util.helper(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatalf("same-package pub(package) access should load: %v", err)
	}
	if main := findFunc(prog, "main"); main == nil || !callsDirect(main, "util__helper") {
		t.Errorf("util.helper() should resolve to util__helper; funcs: %v", funcNames(prog))
	}
}

// A `pub(package)` decl is NOT visible from a module in a DIFFERENT
// package (different directory).
func TestLoadPubPackageCrossPackageRejected(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.fern": `pub(package) function helper(): i32 { return 42; }`,
		"sub/app.fern": `import "../util";
function appmain(): i32 { return util.helper(); }`,
		"main.fern": `import "./sub/app";
function main(): i32 { return 0; }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err == nil {
		t.Fatal("cross-package pub(package) access should error")
	}
	if !strings.Contains(err.Error(), "pub(package)") || !strings.Contains(err.Error(), "helper") {
		t.Errorf("error should explain the package-scope restriction; got %v", err)
	}
}

func TestSamePackage(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"/p/a.fern", "/p/b.fern", true},
		{"/p/a.fern", "/p/q/b.fern", false},
		{"stdlib://std/json.fern", "stdlib://core/int.fern", true},
		{"stdlib://std/json.fern", "/p/a.fern", false},
	}
	for _, c := range cases {
		if got := modload.SamePackageForTest(c.a, c.b); got != c.want {
			t.Errorf("samePackage(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestLoadRejectsPrivateFunctionAccess(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.fern": `function secret(): i32 { return 9; }`,
		"main.fern": `import "./util";
function main(): i32 { return util.secret(); }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err == nil {
		t.Fatal("expected visibility error from Load")
	}
	if !strings.Contains(err.Error(), "util.secret") || !strings.Contains(err.Error(), "not exported") {
		t.Errorf("error should mention `util.secret` and `not exported`; got %v", err)
	}
}

// `pub use "path".{name}` re-exports a public symbol: a consumer of the
// re-exporting module calls `facade.name`, and it rewrites to the
// ORIGINAL module's mangled name (helpers__add5), not facade__add5 (no
// copy is made). See docs/PRELUDE-TO-MODULES.md.
func TestLoadPubUseReexport(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"helpers.fern": `pub function add5(n: i32): i32 { return n + 5; }
pub const BONUS: i32 = 100;`,
		"facade.fern": `pub use "./helpers".{add5, BONUS};`,
		"main.fern": `import "./facade";
function main(): i32 { return facade.add5(10) + facade.BONUS; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatalf("pub use re-export should load: %v", err)
	}
	main := findFunc(prog, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	if !callsDirect(main, "helpers__add5") {
		t.Errorf("facade.add5 should rewrite to helpers__add5 (the original), not facade__add5; funcs: %v", funcNames(prog))
	}
}

// A `pub use` of a transitively-re-exported name resolves through the
// chain to the ultimate original mangled name.
func TestLoadPubUseTransitive(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"helpers.fern": `pub function add5(n: i32): i32 { return n + 5; }`,
		"facade.fern":  `pub use "./helpers".{add5};`,
		"prelude.fern": `pub use "./facade".{add5};`,
		"main.fern": `import "./prelude";
function main(): i32 { return prelude.add5(10); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatalf("transitive pub use should load: %v", err)
	}
	main := findFunc(prog, "main")
	if !callsDirect(main, "helpers__add5") {
		t.Errorf("prelude.add5 should resolve transitively to helpers__add5; funcs: %v", funcNames(prog))
	}
}

// `pub use` can only re-export PUBLIC symbols — re-exporting a private
// one is rejected.
func TestLoadPubUseRejectsPrivate(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"helpers.fern": `function secret(): i32 { return 9; }`,
		"facade.fern":  `pub use "./helpers".{secret};`,
		"main.fern": `import "./facade";
function main(): i32 { return facade.secret(); }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err == nil {
		t.Fatal("re-exporting a private symbol should error")
	}
	if !strings.Contains(err.Error(), "does not export") || !strings.Contains(err.Error(), "secret") {
		t.Errorf("error should mention the missing export `secret`; got %v", err)
	}
}

// A `pub use`-re-exported type resolves through the facade to its
// original module's mangled name (not the facade's prefix), so a
// consumer's `facade.Pt` annotation + literal both land on `helpers__Pt`.
func TestLoadPubUseTypeReexport(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"helpers.fern": `pub struct Pt { x: i32 }`,
		"facade.fern":  `pub use "./helpers".{Pt};`,
		"main.fern": `import "./facade";
function make(): facade.Pt { return facade.Pt { x: 7 }; }
function main(): i32 { return make().x; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatalf("type re-export should load: %v", err)
	}
	mk := findFunc(prog, "make")
	if mk == nil {
		t.Fatal("make not found")
	}
	// Return type annotation `facade.Pt` → helpers__Pt.
	st, ok := mk.ReturnType.(ast.StructType)
	if !ok || st.Name != "helpers__Pt" {
		t.Errorf("make return type = %#v, want StructType{Name: helpers__Pt}", mk.ReturnType)
	}
	// Struct literal `facade.Pt { … }` → helpers__Pt.
	ret, _ := mk.Body.Stmts[0].(*ast.Return)
	if ret == nil {
		t.Fatal("make body should start with a return")
	}
	if sl, ok := ret.Value.(*ast.StructLit); !ok || sl.TypeName != "helpers__Pt" {
		t.Errorf("struct literal = %#v, want StructLit{TypeName: helpers__Pt}", ret.Value)
	}
}

// `pub use` can only re-export PUBLIC types — a private one is rejected.
func TestLoadPubUseRejectsPrivateType(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"helpers.fern": `struct Secret { x: i32 }`,
		"facade.fern":  `pub use "./helpers".{Secret};`,
		"main.fern": `import "./facade";
function main(): i32 { return 0; }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err == nil {
		t.Fatal("re-exporting a private type should error")
	}
	if !strings.Contains(err.Error(), "does not export") || !strings.Contains(err.Error(), "Secret") {
		t.Errorf("error should mention the missing export `Secret`; got %v", err)
	}
}

// Cross-module function-value references (taking a private function
// as a value, not calling it) are equally rejected.
func TestLoadRejectsPrivateFunctionValueReference(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.fern": `function secret(): i32 { return 9; }`,
		"main.fern": `import "./util";
function main(): i32 {
	var f: () => i32 = util.secret;
	return f();
}`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"point.fern": `struct Point { x: i32, y: i32 }`,
		"main.fern": `import "./point";
function main(): i32 {
	var p: point.Point = point.Point { x: 1, y: 2 };
	return p.x;
}`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"util.fern": `function helper(): i32 { return 1; }
pub function exposed(): i32 { return helper() + 1; }`,
		"main.fern": `import "./util";
function main(): i32 { return util.exposed(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"limits.fern": `pub const MAX: i32 = 100;`,
		"main.fern": `import "./limits";
function main(): i32 { return limits.MAX; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"limits.fern": `const MAX: i32 = 100;`,
		"main.fern": `import "./limits";
function main(): i32 { return limits.MAX; }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"util.fern": `pub function f(): i32 { return 1; }`,
		"main.fern": `import "./util";
function main(): i32 { return util.f(); }`,
	})
	_, srcs, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"main.fern": `import "std/_test_empty";
function main(): i32 { return _test_empty.stdlib_test_marker(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"main.fern": `import "core/_test_empty";
function main(): i32 { return _test_empty.core_test_marker(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"main.fern": `import "std/does_not_exist";
function main(): i32 { return 0; }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"util.fern": `pub function f(): i32 { return 1; }`,
		"main.fern": `import "./util";
function main(): i32 { return util.f(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	mainAbs, _ := filepath.Abs(filepath.Join(dir, "main.fern"))
	utilAbs, _ := filepath.Abs(filepath.Join(dir, "util.fern"))
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
		"c.fern": `pub function c_fn(): i32 { return 3; }`,
		"b.fern": `import "./c";
pub function b_fn(): i32 { return c.c_fn() + 2; }`,
		"a.fern": `import "./b";
function main(): i32 { return b.b_fn() + 1; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "a.fern"))
	if err != nil {
		t.Fatal(err)
	}
	aAbs, _ := filepath.Abs(filepath.Join(dir, "a.fern"))
	bAbs, _ := filepath.Abs(filepath.Join(dir, "b.fern"))
	cAbs, _ := filepath.Abs(filepath.Join(dir, "c.fern"))

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

// DirectImports is the same shape as ModuleImports with the
// transitive step removed: a module maps to what its own `import`
// declarations name, plus itself. Trait-method resolution ranks a
// directly-imported trait above one reached only through the closure,
// so the two must not be conflated.
func TestLoadComputesDirectImports(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"c.fern": `pub function c_fn(): i32 { return 3; }`,
		"b.fern": `import "./c";
pub function b_fn(): i32 { return c.c_fn() + 2; }`,
		"a.fern": `import "./b";
function main(): i32 { return b.b_fn() + 1; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "a.fern"))
	if err != nil {
		t.Fatal(err)
	}
	aAbs, _ := filepath.Abs(filepath.Join(dir, "a.fern"))
	bAbs, _ := filepath.Abs(filepath.Join(dir, "b.fern"))
	cAbs, _ := filepath.Abs(filepath.Join(dir, "c.fern"))

	if prog.DirectImports == nil {
		t.Fatal("DirectImports map is nil")
	}
	if !prog.DirectImports[aAbs][aAbs] {
		t.Errorf("direct[a] should contain a itself")
	}
	if !prog.DirectImports[aAbs][bAbs] {
		t.Errorf("direct[a] should contain b")
	}
	// The whole point: c is in a's transitive closure but a never
	// imported it.
	if prog.DirectImports[aAbs][cAbs] {
		t.Errorf("direct[a] should NOT contain c (transitive only)")
	}
	if !prog.ModuleImports[aAbs][cAbs] {
		t.Errorf("closure[a] should still contain c")
	}
	if !prog.DirectImports[bAbs][cAbs] {
		t.Errorf("direct[b] should contain c")
	}
	if prog.DirectImports[cAbs][bAbs] || prog.DirectImports[cAbs][aAbs] {
		t.Errorf("direct[c] should contain only c")
	}
}

// Module-scoped method dispatch (Phase 3 of the prelude-to-
// modules migration): a method declared in module L is callable
// from module M only when M's import closure reaches L.
//
// Importer sees the method:
func TestMethodVisibleAcrossExplicitImport(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"lib.fern": `pub function (n: i32) my_method(): i32 { return n + 1; }`,
		"main.fern": `import "./lib";
function main(): i32 { return (5).my_method(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"lib.fern":  `pub function (n: i32) my_method(): i32 { return n + 1; }`,
		"main.fern": `function main(): i32 { return (5).my_method(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
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
		"c.fern": `pub function (n: i32) deep(): i32 { return n * 3; }`,
		"b.fern": `import "./c";
pub function passthrough(n: i32): i32 { return n.deep(); }`,
		"a.fern": `import "./b";
function main(): i32 { return (4).deep(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "a.fern"))
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
		"main.fern": `import "std/i32";
function main(): i32 { return (0 - 9).abs(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("expected user-import + prelude dedup, got %v", err)
	}
}

// There is no auto-injected prelude — a program needs to
// `import` every stdlib module it uses explicitly. A program
// that DOES need stdlib helpers writes `import "std/foo";` for
// each module it touches.
//
// Positive case: a program that needs no stdlib at all
// type-checks cleanly. Proves nothing is implicitly required.
func TestBareProgramTypechecks(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `
function main(): i32 { return 42; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("expected bare program to compile, got %v", err)
	}
}

// Negative case: a program that doesn't import std/i32. The
// `(5).abs()` call has no `abs` method in scope and the checker
// errors — there's no prelude to silently supply it.
func TestMissingStdlibImportErrors(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `
function main(): i32 { return (5).abs(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatal("expected checker error when std/i32 isn't imported")
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

// Stdlib-to-stdlib method dispatch is universally visible
// without an explicit import between the two stdlib modules.
// The stdlib's method graph has natural cycles (std/string's
// bodies dispatch (i32) byte methods from std/i32; std/i32's
// bodies dispatch (string) methods from std/string), and the
// auto-prelude path already side-steps the visibility gate by
// clearing `SourceModule` on every loaded fn. The shortcut
// preserves that semantics under no-prelude too — std/array
// can call .abs() on an (i32) byte element without std/array's
// source needing `import "std/i32"`.
func TestCheckStdlibToStdlibMethodsVisibleAcrossModules(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		// std/array's `abs_each` body calls `arr[i].abs()` — an
		// (i32) method declared in std/i32. With the visibility
		// shortcut, no explicit `import "std/i32"` is needed
		// inside std/array's source — the user lists both
		// modules and the dispatch resolves regardless of any
		// stdlib-internal import graph. Extra stdlib imports
		// satisfy ancillary method-source visibility needs
		// (std/array body also calls (string).contains, etc.).
		"main.fern": `
import "std/array";
import "std/i32";
import "std/string";
import "std/sort";
function main(): i32 {
    var xs: i32[] = [0 - 3, 4, 0 - 1];
    var ys = xs.abs_each();
    return ys[0] + ys[1] + ys[2];
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("expected stdlib-to-stdlib method visibility, got %v", err)
	}
}

// "Manually hoisted" methods — top-level `pub function
// __method_<Type>_<Name>(...)` decls without a receiver block
// — must NOT pick up the `<mod>__` prefix during cross-module
// load. The checker's auto-discovery pass keys off the
// `__method_` prefix to register them as `Type.<Name>` in the
// Methods map; if modload renamed them to `mod____method_…`
// dispatch would fail.
//
// Repro: explicit `import "std/array";` from a user file. Under
// the buggy prefix every `(arr).avg()` / `.gcd_all()` / etc. method
// call surfaces as "field access on non-struct value of type
// i32[]" because the renamed function never registers as
// `Array.avg` / `Array.gcd_all`.
//
// `avg` is used because it is one of the survivors of the #2663 collapse:
// the element-polymorphic verbs (`sum` / `max` / `count` / …) are now
// written with a real `(xs: T[])` receiver, which the checker hoists
// itself, so they never exercise this path. What still needs it is a
// concrete-receiver `__method_Array_<name>` helper.
func TestLoadPreservesManuallyHoistedMethodNames(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `import "std/array";
function main(): i32 {
    var xs: i32[] = [1, 2, 3, 4, 5];
    match (xs.avg()) { Some(a) => { return a; }, None => { return 0; } }
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if findFunc(prog, "__method_Array_avg") == nil {
		t.Fatalf("__method_Array_avg should keep its bare name under modload; got: %v", funcNames(prog))
	}
	if findFunc(prog, "array____method_Array_avg") != nil {
		t.Errorf("modload should not prefix __method_ names with the module prefix")
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("expected check to succeed (Array.avg dispatch resolves), got %v", err)
	}
}

// Map runtime helpers — `map_new_impl`, `__map_*_impl`,
// `__mapiter_*_impl` — keep their bare names under modload
// because codegen rewrites the high-level `map_new` /
// `__method_Map_*` calls to those concrete targets via
// hardcoded `case` switches. If modload prefixed them to
// `map__map_new_impl` etc., the codegen-emitted call site
// would dangle: the bare-named symbol the assembler emits a
// `bl` to wouldn't resolve at link time.
//
// Repro: a no-prelude program that uses `Map[string, i32]`
// via `import "core/map"` would surface the failure as
// `undefined reference to map_new_impl` from the linker.
func TestLoadPreservesMapRuntimeHelperNames(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("answer", 42);
    return m.get_or("answer", 0);
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"map_new_impl", "__map_set_impl", "__map_get_or_impl"} {
		if findFunc(prog, want) == nil {
			t.Errorf("expected %q to keep its bare name; got: %v", want, funcNames(prog))
		}
	}
	if findFunc(prog, "map__map_new_impl") != nil {
		t.Errorf("modload should not prefix map_new_impl with the module prefix")
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("expected check to succeed (map runtime helpers resolve), got %v", err)
	}
}

// LoadSource must not depend on os.Getwd — it's unimplemented under
// GOOS=js, where the browser playground / cmd/fern-wasm run every
// in-memory compile. Regression guard for the bug where LoadSource
// resolved a relative synthetic entry via filepath.Abs (→ os.Getwd),
// breaking the playground with "getwd: not implemented on js". Simulate
// a missing cwd on linux by chdir'ing into a temp dir and removing it,
// so os.Getwd() errors; LoadSource should still succeed.
func TestLoadSourceDoesNotNeedGetwd(t *testing.T) {
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(orig)
	tmp, err := os.MkdirTemp("", "nowd")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	// Unlink the cwd so os.Getwd() now fails (ENOENT). Best-effort:
	// if the platform refuses, the test still exercises LoadSource.
	_ = os.Remove(tmp)
	if _, err := os.Getwd(); err == nil {
		t.Skip("could not make cwd unresolvable on this platform; skipping getwd guard")
	}

	src := `
import "std/i32";
function main(): i32 { return (5).abs(); }`
	if _, _, err := modload.LoadSource(src); err != nil {
		t.Errorf("LoadSource must not depend on os.Getwd; got: %v", err)
	}
}

// `todo` sites survive the module merge for the ENTRY module only:
// ast.Position carries no filename, so a site from an imported module
// could not be attributed to its file in `-check`'s warning output.
// Pinned here so the contract doesn't silently regress in either
// direction (dropping entry sites, or leaking import sites).
func TestLoadCarriesEntryTodoSitesOnly(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"util.fern": `pub function stubbed(): i32 { todo("in util"); }`,
		"main.fern": `import "./util";
function helper(): i32 {
    todo("in entry");
}
function main(): i32 { return util.stubbed() + helper(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.TodoSites) != 1 {
		t.Fatalf("TodoSites = %d entries, want 1 (entry module only)", len(prog.TodoSites))
	}
	if prog.TodoSites[0].Line != 3 {
		t.Errorf("TodoSites[0].Line = %d, want 3 (the entry's todo)", prog.TodoSites[0].Line)
	}
}
