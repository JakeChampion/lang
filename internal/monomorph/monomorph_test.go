package monomorph_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// TestRunRewritesGenericCallSitesInsideEveryExprShape locks in
// the walker's coverage of expression shapes that can host a
// generic Call. Earlier versions missed MapLit / FString /
// Assign — a generic call buried inside one of these would
// survive un-mangled through Run, and the trailing re-check
// would fail with "undefined identifier <generic-fn>".
func TestRunRewritesGenericCallSitesInsideEveryExprShape(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "MapLit value position",
			src: `
import "core/map";
function id[T](x: T): T { return x; }
function main(): i32 {
    var m: Map[i32, i32] = Map { 1: id(42) };
    return 0;
}`,
		},
		{
			name: "MapLit key position",
			src: `
import "core/map";
function id[T](x: T): T { return x; }
function main(): i32 {
    var m: Map[i32, i32] = Map { id(1): 42 };
    return 0;
}`,
		},
		{
			name: "FString interpolant",
			src: `
import "std/i32";
function id[T](x: T): T { return x; }
function main(): i32 {
    var s: string = f"hello {id(42)} world";
    return 0;
}`,
		},
		{
			name: "Assign rhs",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var n: i32 = 0;
    n = id(7);
    return n;
}`,
		},
		{
			name: "nested FuncDecl body",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    function bump(x: i32): i32 { return id(x) + 1; }
    return bump(41);
}`,
		},
		{
			name: "Lambda body",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var f: (i32) => i32 = function (x: i32): i32 { return id(x) + 1; };
    return f(41);
}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog, _, err := modload.LoadSource(c.src)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatalf("monomorph: %v", err)
			}
			// Confirm the generic decl is gone and a mangled
			// clone took its place — that's the sign the
			// rewrite-and-clone cycle ran end-to-end.
			var sawClone, sawGeneric bool
			for _, fn := range prog.Funcs {
				if fn.Name == "id" {
					sawGeneric = true
				}
				if strings.HasPrefix(fn.Name, "id__") {
					sawClone = true
				}
			}
			if sawGeneric {
				t.Errorf("generic `id` survived in prog.Funcs after monomorph")
			}
			if !sawClone {
				t.Errorf("no `id__*` clone found after monomorph")
			}
		})
	}
}

// TestRunSubstitutesMethodCallTypeArgsInGenericBody locks in the
// fix for a substitution-walker gap: a generic function body that
// calls an Array method (`push` / `len`) on a `T[]` receiver stamps
// the method call's `TypeArgs` to `[T]` during the first check. The
// monomorph clone-substitution walker must rewrite those `TypeArgs`
// to the concrete instantiation (`[i32]`), or the post-monomorph
// re-check substitutes the method signature by `T→T` (a no-op) and
// rejects the concrete element type against the abstract `T[]`.
//
// The bug surfaced because `substituteExpr` had no `*ast.Assign`
// case, so the `push` call inside `out = out.append(x)` (and the
// whole RHS of any reassignment, e.g. inside a `for-in` loop body)
// was never walked. Before the fix, monomorph.Run returned
// "re-check failed (compiler bug): … expected T[], got i32[]".
func TestRunSubstitutesMethodCallTypeArgsInGenericBody(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "push on T[] inside for-in (map)",
			src: `function map_arr[T, U](xs: T[], f: (T) => U): U[] {
    var out: U[] = [];
    for x in xs { out = out.append(f(x)); }
    return out;
}
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    var ys: i32[] = map_arr(xs, function (n: i32): i32 { return n * 10; });
    return ys[0] + ys[1] + ys[2];
}`,
		},
		{
			name: "push-only generic body",
			src: `function dup[T](xs: T[]): T[] {
    var out: T[] = [];
    for x in xs { out = out.append(x); }
    return out;
}
function main(): i32 {
    var xs: i32[] = [5, 7];
    var ys: i32[] = dup(xs);
    return ys[0] + ys[1];
}`,
		},
		{
			name: "len method on T[]",
			src: `function count[T](xs: T[]): i32 {
    var n: i32 = 0;
    for x in xs { n = n + xs.len(); }
    return n;
}
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    return count(xs);
}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog, _, err := modload.LoadSource(c.src)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatalf("monomorph: %v", err)
			}
		})
	}
}

// TestRunHandlesPartiallyInferredGenericCalls — variant
// constructors like `Ok(x)` and `Err(e)` only fix one of
// Result[T, E]'s two type parameters via their payload. The
// other has to come from the surrounding context (var-init
// annotation, function return slot).
//
// Before the destination-refinement work, the checker stamped
// the call's TypeArgs as `[Result{no args}]`, monomorph
// mangled to `pick__Result`, and the cloned param types were
// bare Result. The re-check rejected with "Result has 2 type
// parameter(s), 0 supplied".
//
// This test locks in the fix: each program type-checks and
// monomorphs cleanly. Langsmith's `skipGeneric` workaround in
// internal/fernsmith/fernsmith.go can come back to a simple
// flip once this is solid.
func TestRunHandlesPartiallyInferredGenericCalls(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "pick with Ok+Err args, Result destination annotation",
			src: `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function main(): i32 {
    var r: Result[i32, i32] = pick(true, Ok(1), Err(2));
    return 0;
}`,
		},
		{
			name: "id with Ok arg, Result destination annotation",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var r: Result[i32, i32] = id(Ok(7));
    return 0;
}`,
		},
		{
			name: "pick with None+None args, Option destination annotation",
			src: `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function main(): i32 {
    var o: Option[i32] = pick(true, None, None);
    return 0;
}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog, _, err := modload.LoadSource(c.src)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatalf("monomorph: %v", err)
			}
		})
	}
}

// TestWalkerCoversEveryASTNodeWithChildren is the load-bearing
// "no silent gaps" guarantee for monomorph's call-rewriting
// walker. Through this thread the walker has been missing seven
// shapes that can host a sub-expression (MapLit, FString,
// Assign, Lambda, nested FuncDecl, Defer, Switch) — each one
// surfaced months apart, always as "monomorph: re-check
// failed: undefined identifier <generic-fn>" after a generic
// call buried in the un-walked shape survived the rewrite
// step.
//
// This test enumerates every AST node with sub-expression
// children and asserts that a generic call buried inside is
// found and rewritten by monomorph. Adding a new AST variant
// forces an entry here (or an explicit Skip with a noted
// reason).
//
// Pairs with `allASTNodesWithChildren` below as a tripwire:
// any *ast.Expr / *ast.Stmt that *could* host a child
// expression must either appear in the coverage table or be
// listed in the "intentionally leaf or post-monomorph" skip
// set.
func TestWalkerCoversEveryASTNodeWithChildren(t *testing.T) {
	cases := []struct {
		node string
		src  string
		skip string
	}{
		// ---- Expression shapes with sub-expressions ----
		{node: "Call.Args", src: `function id[T](x: T): T { return x; }
function f(n: i32): i32 { return n; }
function main(): i32 { return f(id(7)); }`},
		{node: "Call.Callee",
			skip: "Call.Callee is always an Ident / FieldAccess; never holds a generic call site itself",
		},
		{node: "Binary", src: `function id[T](x: T): T { return x; }
function main(): i32 { return id(3) + id(4); }`},
		{node: "Unary", src: `function id[T](x: T): T { return x; }
function main(): i32 { return -id(5); }`},
		{node: "Index", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var a: i32[] = [id(1), 2, 3];
    return a[id(0)];
}`},
		{node: "SliceExpr", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var a: i32[] = [1, 2, 3, 4];
    var s: [i32] = a[id(0):id(2)];
    return s.len();
}`},
		{node: "FieldAccess", src: `function id[T](x: T): T { return x; }
struct P { x: i32 }
function main(): i32 { return id(P { x: 7 }).x; }`},
		{node: "TryOp", src: `function id[T](x: T): T { return x; }
function main(): Option[i32] {
    var o: Option[i32] = id(Some(7));
    var v: i32 = o?;
    return Some(v);
}`},
		{node: "IfExpr", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    return if (id(true)) { id(1) } else { id(2) };
}`},
		{node: "MatchExpr", src: `function id[T](x: T): T { return x; }
enum L { Red, Green }
function main(): i32 {
    return match (id(Red)) { Red => id(1), Green => id(2) };
}`},
		{node: "ArrayLit", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var a: i32[] = [id(1), id(2), id(3)];
    return a[0];
}`},
		{node: "TupleLit", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var t: (i32, i32) = (id(3), id(4));
    var (a, b) = t;
    return a + b;
}`},
		{node: "StructLit.Fields", src: `function id[T](x: T): T { return x; }
struct P { x: i32, y: i32 }
function main(): i32 {
    var p: P = P { x: id(1), y: id(2) };
    return p.x + p.y;
}`},
		{node: "MapLit.Key+Value", src: `
import "core/map";
function id[T](x: T): T { return x; }
function main(): i32 {
    var m: Map[i32, i32] = Map { id(1): id(10) };
    return m.len();
}`},
		{node: "FString.Interpolant", src: `
import "std/i32";
function id[T](x: T): T { return x; }
function main(): i32 {
    var s: string = f"x={id(42)}";
    return s.len();
}`},
		{node: "Assign", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var n: i32 = 0;
    n = id(7);
    return n;
}`},
		{node: "Lambda", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var f: (i32) => i32 = function (x: i32): i32 { return id(x) + 1; };
    return f(41);
}`},
		{node: "CastExpr", src: `function id[T](x: T): T { return x; }
function main(): i32 { return (id(7i64) as i32); }`},
		{node: "EnumLit",
			skip: "EnumLit is only constructed (today) for payload-less variants used as bare Idents (Args is always nil); when the checker grows EnumLit-with-Args paths, add a real case and the walker should already handle it via the FieldInit-like recursion",
		},
		{node: "CaptureRef",
			skip: "CaptureRef is closureconv-synthetic; monomorph runs before closureconv and never sees it",
		},
		{node: "MakeClosure",
			skip: "MakeClosure is closureconv-synthetic; monomorph runs before closureconv and never sees it",
		},

		// ---- Statement shapes with sub-expressions ----
		{node: "Var.Init", src: `function id[T](x: T): T { return x; }
function main(): i32 { var n: i32 = id(7); return n; }`},
		{node: "Destructure.Init", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var (a, b) = id((1, 2));
    return a + b;
}`},
		{node: "ExprStmt.Expr", src: `function id[T](x: T): T { return x; }
function main(): i32 { var n: i32 = 0; n = id(7); return n; }`},
		{node: "Return.Value", src: `function id[T](x: T): T { return x; }
function main(): i32 { return id(7); }`},
		{node: "If.Cond+Then+Else", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    if (id(true)) { return id(1); } else { return id(2); }
    return 0;
}`},
		{node: "IfLet.Source", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    if let Some(v) = id(Some(7)) { return v; }
    return -1;
}`},
		{node: "LetElse.Source", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    let Some(v) = id(Some(7)) else { return -1; };
    return v;
}`},
		{node: "While.Cond+Body", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var i: i32 = 0;
    while (id(i) < 3) { i = id(i) + 1; }
    return i;
}`},
		{node: "For.Init+Cond+Step+Body", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var sum: i32 = 0;
    for (var i: i32 = id(0); i < 3; i = id(i) + 1) { sum = id(sum) + i; }
    return sum;
}`},
		{node: "Match.Tag+Arms", src: `function id[T](x: T): T { return x; }
enum L { Red, Green }
function main(): i32 {
    var n: i32 = 0;
    match (id(Red)) {
        Red => { n = id(1); },
        Green => { n = id(2); }
    }
    return n;
}`},
		{node: "FuncDecl.Body (nested)", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    function bump(x: i32): i32 { return id(x) + 1; }
    return bump(41);
}`},
		{node: "Defer.Expr", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var n: i32 = 0;
    defer n = id(100);
    n = 7;
    return n;
}`},
		{node: "Block.Stmts",
			skip: "Block is a list of Stmts; each Stmt is walked individually — the cases above cover the children",
		},
		{node: "Break",
			skip: "Break has no children",
		},
		{node: "Continue",
			skip: "Continue has no children",
		},
	}

	for _, c := range cases {
		t.Run(c.node, func(t *testing.T) {
			if c.skip != "" {
				t.Skipf("not applicable: %s", c.skip)
				return
			}
			if c.src == "" {
				t.Fatalf("%s: missing src", c.node)
			}
			prog, _, err := modload.LoadSource(c.src)
			if err != nil {
				t.Fatalf("load: %v\nsrc:\n%s", err, c.src)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v\nsrc:\n%s", err, c.src)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatalf("monomorph: %v\nsrc:\n%s", err, c.src)
			}
			// Confirm a clone with the mangled name landed.
			// The generic decl `id[T]` should be gone too.
			sawClone := false
			for _, fn := range prog.Funcs {
				if fn.Name == "id" {
					t.Errorf("%s: generic `id` survived in prog.Funcs", c.node)
				}
				if strings.HasPrefix(fn.Name, "id__") {
					sawClone = true
				}
			}
			if !sawClone {
				t.Errorf("%s: no `id__*` clone found — walker missed the call site", c.node)
			}
		})
	}

	// Tripwire: assert the case list covers every ast.Expr /
	// ast.Stmt with sub-expression children. The skip set above
	// counts as coverage (acknowledged not-applicable).
	covered := map[string]bool{}
	for _, c := range cases {
		// Split on `.` so "Call.Args" maps to "Call". A node
		// may appear under multiple specific entries.
		base := c.node
		if i := strings.Index(base, "."); i >= 0 {
			base = base[:i]
		}
		if i := strings.Index(base, " "); i >= 0 {
			base = base[:i]
		}
		covered[base] = true
	}
	for _, name := range allASTNodesWithChildren() {
		if !covered[name] {
			t.Errorf("missing walker coverage for ast.%s — add to TestWalkerCoversEveryASTNodeWithChildren or mark Skip with a reason", name)
		}
	}
}

// allASTNodesWithChildren enumerates every Expr / Stmt
// implementation whose body has sub-expression children that
// monomorph's walker needs to recurse into. Leaf nodes (Ident,
// NumberLit, BoolLit, etc.) are excluded — they don't host
// Call sites.
//
// Compile-time-checked: each entry is a `(*ast.NodeType)(nil)`
// pointer, so removing the type from the package fails the
// build right here.
func allASTNodesWithChildren() []string {
	exprs := []ast.Expr{
		(*ast.CastExpr)(nil),
		(*ast.FString)(nil),
		(*ast.ArrayLit)(nil),
		(*ast.Index)(nil),
		(*ast.SliceExpr)(nil),
		(*ast.Call)(nil),
		(*ast.Binary)(nil),
		(*ast.Unary)(nil),
		(*ast.Assign)(nil),
		(*ast.IfExpr)(nil),
		(*ast.MatchExpr)(nil),
		(*ast.TryOp)(nil),
		(*ast.StructLit)(nil),
		(*ast.TupleLit)(nil),
		(*ast.MapLit)(nil),
		(*ast.FieldAccess)(nil),
		(*ast.EnumLit)(nil),
		(*ast.CaptureRef)(nil),
		(*ast.MakeClosure)(nil),
		(*ast.Lambda)(nil),
	}
	stmts := []ast.Stmt{
		(*ast.Block)(nil),
		(*ast.If)(nil),
		(*ast.IfLet)(nil),
		(*ast.LetElse)(nil),
		(*ast.While)(nil),
		(*ast.For)(nil),
		(*ast.Break)(nil),
		(*ast.Continue)(nil),
		(*ast.Return)(nil),
		(*ast.Defer)(nil),
		(*ast.Var)(nil),
		(*ast.Destructure)(nil),
		(*ast.ExprStmt)(nil),
		(*ast.Match)(nil),
		(*ast.FuncDecl)(nil),
	}
	out := make([]string, 0, len(exprs)+len(stmts))
	for _, e := range exprs {
		out = append(out, trimPkg(fmt.Sprintf("%T", e)))
	}
	for _, s := range stmts {
		out = append(out, trimPkg(fmt.Sprintf("%T", s)))
	}
	return out
}

func trimPkg(s string) string {
	s = strings.TrimPrefix(s, "*")
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

// TestMonomorphTupleArgProducesAssemblerSafeName locks in that a
// generic function/struct instantiated with a TUPLE type argument
// mangles to a symbol containing no parentheses. A tuple type's
// String() is `(i32, i32)`; sanitize() previously left the `(`/`)`
// intact, so the clone name `id__(i32__i32)` reached the native
// assemblers and failed to assemble (wasm tolerated it). The
// emitted clone names must be `[A-Za-z0-9_]`-only.
// A polymorphically-recursive generic struct (`Nest[T]` whose field is
// typed `Nest[Nest[T]]`) demands an unbounded family of instantiations,
// so monomorphisation can't terminate. It must report a clear
// "did not terminate / infinitely recursive" error rather than running
// off the round cap and failing the trailing re-check with a misleading
// "compiler bug". Regression for I3 in docs/ADVERSARIAL-REVIEW-2026-06.md.
func TestRunReportsPolymorphicRecursion(t *testing.T) {
	src := `struct Nest[T] { head: T, tail: Nest[Nest[T]] }
function f(n: Nest[i32]): i32 { return n.head; }
function main(): i32 { return 0; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		// If a future checker rule pre-rejects polymorphic recursion,
		// that's an acceptable place to catch it — but then this test
		// should move to the checker. For now the checker accepts it and
		// monomorph is the gate.
		t.Skipf("checker rejected the recursive generic (moved earlier): %v", err)
	}
	err = monomorph.Run(prog, info)
	if err == nil {
		t.Fatal("expected monomorph to reject polymorphically-recursive generic struct")
	}
	if strings.Contains(err.Error(), "compiler bug") {
		t.Errorf("error should be a clear non-termination diagnostic, not a 'compiler bug': %v", err)
	}
	if !strings.Contains(err.Error(), "did not terminate") {
		t.Errorf("error should explain non-termination, got: %v", err)
	}
}

func TestMonomorphTupleArgProducesAssemblerSafeName(t *testing.T) {
	src := `function id[T](x: T): T { return x; }
function main(): i32 {
    var t = id((3, 4));
    return t.0 + t.1;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	sawClone := false
	for _, fn := range prog.Funcs {
		if !strings.HasPrefix(fn.Name, "id__") {
			continue
		}
		sawClone = true
		for i := 0; i < len(fn.Name); i++ {
			c := fn.Name[i]
			ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
				(c >= '0' && c <= '9') || c == '_'
			if !ok {
				t.Errorf("clone name %q contains assembler-unsafe byte %q", fn.Name, string(c))
			}
		}
	}
	if !sawClone {
		t.Fatalf("no `id__*` clone produced for id((3,4))")
	}
}

// TestMonomorphNestedGenericNameIsConsistent locks in that a
// nested generic instantiation like `Box[Box[i32]]` mangles to the
// SAME name whether the outer struct's type arg arrives raw
// (StructType{Name:"Box", Args:[i32]}) or pre-rewritten
// (StructType{Name:"Box__i32"}). A mismatch previously surfaced as
// a monomorph re-check failure: "cannot assign Box__Box_i32_ to
// variable of type Box__Box__i32".
func TestMonomorphNestedGenericNameIsConsistent(t *testing.T) {
	src := `struct Box[T] { v: T }
function main(): i32 {
    var b: Box[Box[i32]] = Box { v: Box { v: 42 } };
    return b.v.v;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph (nested generic name mismatch): %v", err)
	}
}

// TestRunSubstitutesArrayLiteralElemTypeAtCallSite locks in the fix
// for a codegen-corrupting gap: an array literal passed to a generic
// `T[]` parameter is stamped by the checker with ElemType = the type
// parameter (e.g. `T`). The caller isn't a generic clone, so the
// clone-substitution walker never reached it, and the unsubstituted
// ParamType drove the wrong per-element store width at codegen — a
// pointer-element array (struct[]) got single-word stores into
// pointer-width slots, corrupting the array on drop. monomorph.Run
// must substitute the concrete instantiation into the argument's
// ElemType at the call site.
func TestRunSubstitutesArrayLiteralElemTypeAtCallSite(t *testing.T) {
	src := `
import "std/i32";
struct Box { v: i32 }
function len_of[T](xs: T[]): i32 { return xs.len(); }
function main(): i32 {
    return len_of([Box{v: 1}, Box{v: 2}]);
}`
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	// Find the array-literal argument in main and confirm its
	// ElemType is the concrete StructType, not a leftover ParamType.
	var lit *ast.ArrayLit
	for _, fn := range prog.Funcs {
		if fn.Name != "main" {
			continue
		}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			if al, ok := n.(*ast.ArrayLit); ok {
				lit = al
			}
			return true
		})
	}
	if lit == nil {
		t.Fatal("array literal not found in main")
	}
	if _, isParam := lit.ElemType.(ast.ParamType); isParam {
		t.Errorf("array literal ElemType is still a ParamType after monomorph: %#v", lit.ElemType)
	}
	if _, isStruct := lit.ElemType.(ast.StructType); !isStruct {
		t.Errorf("array literal ElemType = %T, want ast.StructType (Box)", lit.ElemType)
	}
}

// TestRunTransitiveInstantiation locks in transitive monomorphisation:
// a generic function whose body calls another generic (`wrap[T]`
// calling `id[T]`) must instantiate the callee too. Before the
// worklist fixpoint, the cloned `wrap[i32]` body still called the
// generic `id` (which gets removed after the pass), and the
// post-monomorph re-check failed with "expected T, got i32".
func TestRunTransitiveInstantiation(t *testing.T) {
	src := `
import "std/i32";
function id[T](x: T): T { return x; }
function wrap[T](x: T): T { return id(x); }
function main(): i32 { return wrap(5); }`
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	// Both wrap and id must have an i32 clone, and no generic decl
	// may survive.
	var ids, wraps, generics int
	for _, fn := range prog.Funcs {
		switch {
		case fn.Name == "id" || fn.Name == "wrap":
			generics++
		case strings.HasPrefix(fn.Name, "id__"):
			ids++
		case strings.HasPrefix(fn.Name, "wrap__"):
			wraps++
		}
	}
	if generics != 0 {
		t.Errorf("a generic decl survived monomorph (%d)", generics)
	}
	if ids == 0 {
		t.Errorf("no id__* clone — transitive instantiation didn't run")
	}
	if wraps == 0 {
		t.Errorf("no wrap__* clone")
	}
}

// TestGenericDeriveDefaultMonomorphises guards the fix for the
// "re-check failed: undefined identifier T/i32" crash: a generic struct's
// derived `default()` is a receiver-less associated function whose call site
// `Box.default()` must (a) get its type args inferred from the binding's
// destination type and (b) have its `T.default()` body rewritten to the
// concrete type — including a PRIMITIVE type param resolving onto a primitive
// `impl Default for i32`. Each case must check + monomorphise without error
// and leave no generic decl behind.
func TestGenericDeriveDefaultMonomorphises(t *testing.T) {
	cases := []struct{ name, src string }{
		{"struct-param", `trait Default { function default(): Self; }
@derive(Default) struct Inner { n: i32 }
@derive(Default) struct Box[T] { v: T }
function main(): i32 { var b: Box[Inner] = Box.default(); return b.v.n; }`},
		{"primitive-param", `trait Default { function default(): Self; }
impl Default for i32 { function default(): i32 { return 0; } }
@derive(Default) struct Box[T] { v: T }
function main(): i32 { var b: Box[i32] = Box.default(); return b.v; }`},
		{"string-param", `trait Default { function default(): Self; }
impl Default for string { function default(): string { return ""; } }
@derive(Default) struct Box[T] { v: T }
function main(): i32 { var b: Box[string] = Box.default(); return b.v.len(); }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog, _, err := modload.LoadSource(c.src)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatalf("monomorph: %v", err)
			}
			for _, fn := range prog.Funcs {
				if len(fn.TypeParams) > 0 {
					t.Errorf("generic decl %q survived monomorph", fn.Name)
				}
			}
		})
	}
}

// TestAssocCallRewriteNamesEveryScalarWidth walks a `T.default()`
// associated call through every scalar a type argument can bind to. The
// rewrite names `T`'s impl with the same classifier the checker
// registers `impl Default for <scalar>` under (`ast.ReceiverTypeName`);
// when the two disagreed the call resolved onto ANOTHER type's impl and
// the trailing re-check died with a "compiler bug".
//
// `u8` is the case that motivated the table: it used to fall through a
// width switch that only knew 32 and 64, so a byte was named `u32` here
// while the checker named it `u8`, and `zero[u8]()` failed with
// "undefined identifier u32" — or, once a u32 impl existed to land on,
// "function returns u8 but expression is u32". Every width is listed so
// the next one to arrive is a one-line addition, not another skew.
func TestAssocCallRewriteNamesEveryScalarWidth(t *testing.T) {
	scalars := []struct{ ty, zero, use string }{
		{"i32", "0", "b"},
		{"i64", "0 as i64", "b as i32"},
		{"u32", "0 as u32", "b as i32"},
		{"u64", "0 as u64", "b as i32"},
		{"u8", "0 as u8", "b as i32"},
		{"f32", "0.0 as f32", "b as i32"},
		{"f64", "0.0", "b as i32"},
		{"boolean", "false", "0"},
		{"string", `""`, "b.len()"},
	}
	for _, s := range scalars {
		t.Run(s.ty, func(t *testing.T) {
			src := fmt.Sprintf(`trait Default { function default(): Self; }
impl Default for %[1]s { function default(): %[1]s { return %[2]s; } }
function zero[T: Default](): T { return T.default(); }
function main(): i32 { var b: %[1]s = zero[%[1]s](); return %[3]s; }`, s.ty, s.zero, s.use)
			prog, _, err := modload.LoadSource(src)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatalf("monomorph: %v", err)
			}
			for _, fn := range prog.Funcs {
				if len(fn.TypeParams) > 0 {
					t.Errorf("generic decl %q survived monomorph", fn.Name)
				}
			}
		})
	}
}
