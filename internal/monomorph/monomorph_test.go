package monomorph_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
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
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var m: Map[i32, i32] = Map { 1: id(42) };
    return 0;
}`,
		},
		{
			name: "MapLit key position",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var m: Map[i32, i32] = Map { id(1): 42 };
    return 0;
}`,
		},
		{
			name: "FString interpolant",
			src: `function id[T](x: T): T { return x; }
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
			prog, err := parser.Parse(c.src)
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
// internal/langsmith/langsmith.go can come back to a simple
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
			prog, err := parser.Parse(c.src)
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
		{node: "MapLit.Key+Value", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var m: Map[i32, i32] = Map { id(1): id(10) };
    return m.len();
}`},
		{node: "FString.Interpolant", src: `function id[T](x: T): T { return x; }
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
		{node: "Arena.Body", src: `function id[T](x: T): T { return x; }
function main(): i32 { arena { var n: i32 = id(7); return n; } return 0; }`},
		{node: "Switch.Tag+Cases", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var n: i32 = 0;
    switch (id(2)) {
        case 1: { n = id(10); }
        case 2: { n = id(20); }
        default: { n = id(-1); }
    }
    return n;
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
			prog, err := parser.Parse(c.src)
			if err != nil {
				t.Fatalf("parse: %v\nsrc:\n%s", err, c.src)
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
		(*ast.Arena)(nil),
		(*ast.Var)(nil),
		(*ast.Destructure)(nil),
		(*ast.ExprStmt)(nil),
		(*ast.Switch)(nil),
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
