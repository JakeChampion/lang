package monomorph_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

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
    let m: Map[i32, i32] = Map { 1: id(42) };
    return 0;
}`,
		},
		{
			name: "MapLit key position",
			src: `
import "core/map";
function id[T](x: T): T { return x; }
function main(): i32 {
    let m: Map[i32, i32] = Map { id(1): 42 };
    return 0;
}`,
		},
		{
			name: "FString interpolant",
			src: `
import "std/i32";
function id[T](x: T): T { return x; }
function main(): i32 {
    let s: string = f"hello {id(42)} world";
    return 0;
}`,
		},
		{
			name: "Assign rhs",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    let n: i32 = 0;
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
			name: "match arm guard",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    let n: i32 = 3;
    match (n) {
        1 when id(n) > 2 => { return 1; },
        _ => { return 0; },
    }
}`,
		},
		{
			name: "enum variant payload",
			src: `enum Wrap { One(i32), None }
function id[T](x: T): T { return x; }
function main(): i32 {
    let w: Wrap = One(id(5));
    match (w) { One(v) => { return v; }, None => { return 0; }, }
}`,
		},
		{
			name: "for-in iterable",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    let total: i32 = 0;
    for x in id([1, 2, 3]) { total = total + x; }
    return total;
}`,
		},
		{
			name: "Lambda body",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    let f: (i32) => i32 = (x: i32): i32 => { return id(x) + 1; };
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

func TestRunRewritesGenericStructLitsInEveryPosition(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "struct-update base",
			src: `struct Box[T] { v: T }
function rewrap[T](x: T): Box[T] { return Box { ...Box { v: x }, v: x }; }
function main(): i32 {
    let a: Box[i32] = rewrap(1);
    let b: Box[boolean] = rewrap(true);
    if (b.v) { return a.v; }
    return 0;
}`,
		},
		{
			name: "f-string interpolant",
			src: `import "std/i32";
struct Box[T] { v: T }
function show[T](x: T): string { return f"{Box { v: 42 }.v}"; }
function main(): i32 {
    let s: string = show(1);
    let t: string = show(true);
    return s.len() - t.len();
}`,
		},
		{
			name: "defer body",
			src: `struct Box[T] { v: T }
function run[T](x: T): i32 {
    let r: i32 = 0;
    defer { let h: Box[i32] = Box { v: 5 }; r = h.v; }
    return 7;
}
function main(): i32 { return run(1) + run(true); }`,
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

func TestRunSubstitutesMethodCallTypeArgsInGenericBody(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "push on T[] inside for-in (map)",
			src: `function map_arr[T, U](xs: T[], f: (T) => U): U[] {
    let out: U[] = [];
    for x in xs { out = out.append(f(x)); }
    return out;
}
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let ys: i32[] = map_arr(xs, (n: i32): i32 => { return n * 10; });
    return ys[0] + ys[1] + ys[2];
}`,
		},
		{
			name: "push-only generic body",
			src: `function dup[T](xs: T[]): T[] {
    let out: T[] = [];
    for x in xs { out = out.append(x); }
    return out;
}
function main(): i32 {
    let xs: i32[] = [5, 7];
    let ys: i32[] = dup(xs);
    return ys[0] + ys[1];
}`,
		},
		{
			name: "len method on T[]",
			src: `function count[T](xs: T[]): i32 {
    let n: i32 = 0;
    for x in xs { n = n + xs.len(); }
    return n;
}
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
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

func TestRunHandlesPartiallyInferredGenericCalls(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "pick with Ok+Err args, Result destination annotation",
			src: `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function main(): i32 {
    let r: Result[i32, i32] = pick(true, Ok(1), Err(2));
    return 0;
}`,
		},
		{
			name: "id with Ok arg, Result destination annotation",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    let r: Result[i32, i32] = id(Ok(7));
    return 0;
}`,
		},
		{
			name: "pick with None+None args, Option destination annotation",
			src: `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function main(): i32 {
    let o: Option[i32] = pick(true, None, None);
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

func TestWalkerCoversEveryASTNodeWithChildren(t *testing.T) {
	cases := []struct {
		node string
		src  string
		skip string
	}{
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
    let a: i32[] = [id(1), 2, 3];
    return a[id(0)];
}`},
		{node: "SliceExpr", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    let a: i32[] = [1, 2, 3, 4];
    let s: [i32] = a[id(0):id(2)];
    return s.len();
}`},
		{node: "FieldAccess", src: `function id[T](x: T): T { return x; }
struct P { x: i32 }
function main(): i32 { return id(P { x: 7 }).x; }`},
		{node: "TryOp", src: `function id[T](x: T): T { return x; }
function run(): Option[i32] {
    let o: Option[i32] = id(Some(7));
    let v: i32 = o?;
    return Some(v);
}
function main(): i32 { if let Some(v) = run() { return v; } return 0; }`},
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
    let a: i32[] = [id(1), id(2), id(3)];
    return a[0];
}`},
		{node: "TupleLit", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    let t: (i32, i32) = (id(3), id(4));
    let (a, b) = t;
    return a + b;
}`},
		{node: "StructLit.Fields", src: `function id[T](x: T): T { return x; }
struct P { x: i32, y: i32 }
function main(): i32 {
    let p: P = P { x: id(1), y: id(2) };
    return p.x + p.y;
}`},
		{node: "MapLit.Key+Value", src: `
import "core/map";
function id[T](x: T): T { return x; }
function main(): i32 {
    let m: Map[i32, i32] = Map { id(1): id(10) };
    return m.len();
}`},
		{node: "FString.Interpolant", src: `
import "std/i32";
function id[T](x: T): T { return x; }
function main(): i32 {
    let s: string = f"x={id(42)}";
    return s.len();
}`},
		{node: "Assign", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    let n: i32 = 0;
    n = id(7);
    return n;
}`},
		{node: "Lambda", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    let f: (i32) => i32 = (x: i32): i32 => { return id(x) + 1; };
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

		{node: "Var.Init", src: `function id[T](x: T): T { return x; }
function main(): i32 { let n: i32 = id(7); return n; }`},
		{node: "Destructure.Init", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    let (a, b) = id((1, 2));
    return a + b;
}`},
		{node: "ExprStmt.Expr", src: `function id[T](x: T): T { return x; }
function main(): i32 { let n: i32 = 0; n = id(7); return n; }`},
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
    let i: i32 = 0;
    while (id(i) < 3) { i = id(i) + 1; }
    return i;
}`},
		{node: "For.Init+Cond+Step+Body", src: `function id[T](x: T): T { return x; }
function main(): i32 {
    let sum: i32 = 0;
    for (let i: i32 = id(0); i < 3; i = id(i) + 1) { sum = id(sum) + i; }
    return sum;
}`},
		{node: "Match.Tag+Arms", src: `function id[T](x: T): T { return x; }
enum L { Red, Green }
function main(): i32 {
    let n: i32 = 0;
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
    let n: i32 = 0;
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

	covered := map[string]bool{}
	for _, c := range cases {
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

func TestRunRecheckKeepsTheTarget(t *testing.T) {
	prog, err := parser.Parse(`function read[T](x: T, name: string): Option[string] { return config_get(name); }
function main(): i32 { match (read(1, "GREETING")) { Some(v) => { return 0; }, None => { return 1; } } }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.CheckTarget(prog, "wasm32-wasi-http")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if call, ok := n.(*ast.Call); ok {
			if id, ok := call.Callee.(*ast.Ident); ok && id.Name == "env" {
				t.Errorf("the re-check renamed config_get to env on the proxy world")
			}
		}
		return true
	})
}

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

func TestMonomorphEntersNestedFunctionAndLambdaBodies(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"nested-fn-type-param", `function apply[T](acc: T, f: (i32, T) => T): T { return f(1, acc); }
function outer[T](acc: T): T {
  function skip(n: i32, a: T): T { return a; }
  return apply(acc, skip);
}
function main(): i32 { return outer(7); }`},
		{"nested-fn-struct-lit", `struct Box[T] { v: T }
function outer(n: i32): i32 {
  function inner(k: i32): i32 { return Box { v: k }.v; }
  return inner(n);
}
function main(): i32 { return outer(7); }`},
		{"nested-fn-annotation", `struct Box[T] { v: T }
function outer(n: i32): i32 {
  function inner(k: i32): i32 {
    let b: Box[i32] = Box { v: k };
    return b.v;
  }
  return inner(n);
}
function main(): i32 { return outer(7); }`},
		{"lambda-annotation", `struct Box[T] { v: T }
function outer(n: i32): i32 {
  let f = ((k: i32) => {
    let c: Box[i32] = Box { v: k };
    c.v
  });
  return f(n);
}
function main(): i32 { return outer(7); }`},
		{"two-instantiations", `function apply[T](acc: T, f: (i32, T) => T): T { return f(1, acc); }
function outer[T](acc: T): T {
  function skip(n: i32, a: T): T { return a; }
  return apply(acc, skip);
}
function main(): i32 {
  let a: i32 = outer(7);
  let b: string[] = outer(["x", "y"]);
  return a + b.len();
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
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

func TestMonomorphEntersDeferAndLoopBodies(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"defer-struct-lit", `struct Holder[T] { v: T }
function run(): i32 {
  let r: i32 = 0;
  defer { let h: Holder[i32] = Holder { v: 5 }; r = h.v; }
  return 7;
}
function main(): i32 { return run(); }`},
		{"defer-in-generic", `struct Box[T] { v: T }
function outer[T](x: T): i32 {
  let r: i32 = 0;
  defer { let b: Box[T] = Box { v: x }; r = 1; }
  return 7;
}
function main(): i32 { return outer(3); }`},
		{"defer-nested", `struct Box[T] { v: T }
function run(n: i32): i32 {
  let r: i32 = 0;
  if (n > 0) { defer { let h: Box[i32] = Box { v: 5 }; r = h.v; } }
  return 7;
}
function main(): i32 { return run(1); }`},
		{"defer-two-insts", `struct Box[T] { v: T }
function outer[T](x: T): i32 {
  let r: i32 = 0;
  defer { let b: Box[T] = Box { v: x }; r = 1; }
  return 7;
}
function main(): i32 {
  let a: i32 = outer(3);
  let b: i32 = outer("s");
  return a + b - 7;
}`},
		{"loop-two-insts", `struct Box[T] { v: T }
function outer[T](x: T): i32 {
  let n: i32 = 0;
  loop {
    let b: Box[T] = Box { v: x };
    n = n + 1;
    if (n > 0) { break; }
  }
  return 7;
}
function main(): i32 {
  let a: i32 = outer(3);
  let b: i32 = outer("s");
  return a + b - 7;
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
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

func TestMonomorphTupleArgProducesAssemblerSafeName(t *testing.T) {
	src := `function id[T](x: T): T { return x; }
function main(): i32 {
    let t = id((3, 4));
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

func TestMonomorphNestedGenericNameIsConsistent(t *testing.T) {
	src := `struct Box[T] { v: T }
function main(): i32 {
    let b: Box[Box[i32]] = Box { v: Box { v: 42 } };
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

func TestGenericDeriveDefaultMonomorphises(t *testing.T) {
	cases := []struct{ name, src string }{
		{"struct-param", `trait Default { function default(): Self; }
@derive(Default) struct Inner { n: i32 }
@derive(Default) struct Box[T] { v: T }
function main(): i32 { let b: Box[Inner] = Box.default(); return b.v.n; }`},
		{"primitive-param", `trait Default { function default(): Self; }
impl Default for i32 { function default(): i32 { return 0; } }
@derive(Default) struct Box[T] { v: T }
function main(): i32 { let b: Box[i32] = Box.default(); return b.v; }`},
		{"string-param", `trait Default { function default(): Self; }
impl Default for string { function default(): string { return ""; } }
@derive(Default) struct Box[T] { v: T }
function main(): i32 { let b: Box[string] = Box.default(); return b.v.len(); }`},
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
function main(): i32 { let b: %[1]s = zero[%[1]s](); return %[3]s; }`, s.ty, s.zero, s.use)
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

func TestRunAcceptsPolymorphicFloatBinaryArg(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "unbounded param, non-generic return",
			src: `function one[A](a: A): string { return "x"; }
function main(): i32 { print(one(1.5 + 2.5)); return 0; }`,
		},
		{
			name: "Display-bounded param",
			src: `import "core/cmp";
function one[A: cmp.Display](a: A): string { return a.to_string(); }
function main(): i32 { print(one(1.5 * 2.0)); return 0; }`,
		},
		{
			name: "float and integer instantiations together",
			src: `import "core/cmp";
function one[A: cmp.Display](a: A): string { return a.to_string(); }
function main(): i32 { print(one(1 + 2)); print(one(1.5 - 0.25)); return 0; }`,
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

func TestRunRewritesSubstitutedCallTypeArgsAgainstEnumClone(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"array-method-on-generic-enum-array-param", `
enum H[T] { Leaf(T), Br(H[T][]) }
function count[T](kids: H[T][]): i32 { return kids.len(); }
function main(): i32 {
  let a: H[i32] = Leaf(1);
  let xs: H[i32][] = [a, Leaf(2)];
  return count(xs);
}`},
		{"match-binding-array-indexed-and-recursed", `
enum H[T] { Leaf(T), Br(H[T][]) }
function depth[T](n: H[T]): i32 {
  match (n) {
    Leaf(x) => { return 1; },
    Br(kids) => {
      let d: i32 = 0;
      let i: i32 = 0;
      while (i < kids.len()) { let k: i32 = depth(kids[i]); if (k > d) { d = k; } i = i + 1; }
      return d + 1;
    },
  }
}
function main(): i32 {
  let a: H[i32] = Leaf(1);
  let b: H[i32] = Br([a, Leaf(2)]);
  return depth(Br([b]));
}`},
		{"for-in-over-match-binding-array", `
enum H[T] { Leaf(T), Br(H[T][]) }
function depth[T](n: H[T]): i32 {
  match (n) {
    Leaf(x) => { return 1; },
    Br(kids) => { let d: i32 = 0; for k in kids { let q: i32 = depth(k); if (q > d) { d = q; } } return d + 1; },
  }
}
function main(): i32 {
  let b: H[i32] = Br([Leaf(1), Leaf(2)]);
  return depth(Br([b]));
}`},
		{"array-literal-of-clone-inside-generic-body", `
enum H[T] { Leaf(T), Br(H[T][]) }
function wrap[T](x: H[T]): H[T] { let kids: H[T][] = [x]; return Br(kids); }
function main(): i32 {
  let w: H[i32] = wrap(Leaf(3));
  match (w) { Br(kids) => { return kids.len(); }, Leaf(v) => { return 0; } }
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
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

func TestRunKeepsValueReceiverSpelledLikeTypeParam(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"parameter", `import "core/cmp";
function same[T: cmp.Eq](T: T, u: T): i32 { if (T.eq(u)) { return 1; } return 0; }
function main(): i32 { return same(3, 3) + same("a", "b"); }`},
		{"local", `import "core/cmp";
function same[T: cmp.Eq](a: T, u: T): i32 { let T: T = a; if (T.eq(u)) { return 1; } return 0; }
function main(): i32 { return same(3, 3) + same("a", "b"); }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, _, err := modload.LoadSource(tc.src)
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

func TestRunRoutesBoundedAssocCallToTheBound(t *testing.T) {
	const traits = `struct P { x: i32 }
trait A { function zero(): Self; }
trait B { function zero(): Self; }
impl A for P { function zero(): Self { return P { x: 1 }; } }
impl B for P { function zero(): Self { return P { x: 2 }; } }
`
	prog, _, err := modload.LoadSource(traits + `function via_a[T: A](): T { return T.zero(); }
function via_b[T: B](): T { return T.zero(); }
function main(): i32 { return via_a[P]().x + via_b[P]().x; }`)
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
	want := map[string]string{"via_a": info.TraitMethods["A.P.zero"], "via_b": info.TraitMethods["B.P.zero"]}
	for _, fn := range prog.Funcs {
		for prefix, provider := range want {
			if !strings.HasPrefix(fn.Name, prefix) || len(fn.TypeParams) > 0 {
				continue
			}
			var called []string
			ast.WalkProgram(&ast.Program{Funcs: []*ast.FuncDecl{fn}}, func(n ast.Node) bool {
				if call, ok := n.(*ast.Call); ok {
					if id, ok := call.Callee.(*ast.Ident); ok {
						called = append(called, id.Name)
					}
				}
				return true
			})
			if len(called) != 1 || called[0] != provider {
				t.Errorf("%s calls %v, want [%s]", fn.Name, called, provider)
			}
			delete(want, prefix)
		}
	}
	if len(want) > 0 {
		t.Errorf("no clone of %v", want)
	}

	prog, _, err = modload.LoadSource(traits + `function main(): i32 { return P.zero().x; }`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := checker.Check(prog); err == nil || !strings.Contains(err.Error(), `ambiguous method "zero" on P`) {
		t.Errorf("a concrete P.zero() wants E074, got %v", err)
	}
}

func TestRunKeepsGenericEnumConformance(t *testing.T) {
	src := `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
enum Slot[T] { Empty, One(T) }
function main(): i32 {
    let m: Map[Slot[i32], i32] = map_new(4);
    m = m.insert(Slot.One(3), 1);
    let e: Slot[i32] = Slot.Empty;
    m = m.insert(e, 2);
    return m.get_or(Slot.One(3), 0) + m.len();
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
}
