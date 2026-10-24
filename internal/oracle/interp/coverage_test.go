package interp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestInterpHandlesEveryASTNode(t *testing.T) {
	cases := []struct {
		node string
		src  string
		skip string
	}{
		{node: "NumberLit", src: `function main(): i32 { return 42; }`},
		{node: "CastExpr", src: `function main(): i64 { let n: i32 = 7; return n as i64; }`},
		{node: "BoolLit", src: `function main(): boolean { return true; }`},
		{node: "StringLit", src: `function main(): i32 { let s: string = "hi"; return s.len(); }`},
		{node: "FString", src: `
import "std/i32";
function main(): i32 { let s: string = f"x={42}"; return s.len(); }`},
		{node: "FloatLit", src: `function main(): i32 { let x: f64 = 1.5; return x as i32; }`},
		{node: "Ident", src: `function main(): i32 { let x: i32 = 7; return x; }`},
		{node: "ArrayLit", src: `function main(): i32 { let a: i32[] = [1, 2, 3]; return a[0]; }`},
		{node: "Index", src: `function main(): i32 { let a: i32[] = [10, 20]; return a[1]; }`},
		{node: "SliceExpr", src: `function main(): i32 { let a: i32[] = [1, 2, 3, 4]; let s: [i32] = a[1:3]; return s.len(); }`},
		{node: "Call", src: `function f(): i32 { return 1; } function main(): i32 { return f(); }`},
		{node: "Binary", src: `function main(): i32 { return 1 + 2; }`},
		{node: "Unary", src: `function main(): i32 { let n: i32 = 5; return -n; }`},
		{node: "Assign", src: `function main(): i32 { let n: i32 = 0; n = 7; return n; }`},
		{node: "IfExpr", src: `function main(): i32 { return if (true) { 1 } else { 2 }; }`},
		{node: "MatchExpr", src: `enum Light { Red, Green }
function main(): i32 {
    let l: Light = Red;
    return match (l) { Red => 1, Green => 2 };
}`},
		{node: "TryOp", src: `function take(o: Option[i32]): Option[i32] {
let n = o?; return Some(n); }
function main(): i32 { match (take(Some(7))) {
Some(n) => { return n; }, None => { return 0; } } return 0; }`},
		{node: "StructLit", src: `struct P { x: i32, y: i32 }
function main(): i32 { let p: P = P { x: 3, y: 4 }; return p.x; }`},
		{node: "TupleLit", src: `function main(): i32 { let t: (i32, i32) = (3, 4); let (a, b) = t; return a + b; }`},
		{node: "MapLit", src: `
import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = Map { 1: 10 };
    return m.len();
}`},
		{node: "FieldAccess", src: `struct P { x: i32, y: i32 }
function main(): i32 { let p: P = P { x: 3, y: 4 }; return p.y; }`},
		{node: "EnumLit",
			skip: "EnumLit nodes are synthetic — the checker rewrites Call/Ident sites to EnumLit only in some paths; the variant-Ident and variant-Call shapes are covered by other cases above",
		},
		{node: "CaptureRef",
			skip: "CaptureRef is closureconv-synthetic; interp doesn't run closureconv (it does direct env capture via Closure values instead)",
		},
		{node: "MakeClosure",
			skip: "MakeClosure is closureconv-synthetic; interp builds Closure values directly from FuncDecl / Lambda",
		},
		{node: "Lambda", src: `function main(): i32 {
    let add1: (i32) => i32 = (x: i32): i32 => { return x + 1; };
    return add1(41);
}`},
		{node: "Block", src: `function main(): i32 { { let n: i32 = 7; return n; } }`},
		{node: "If", src: `function main(): i32 { if (true) { return 1; } return 2; }`},
		{node: "IfLet", src: `function main(): i32 {
    let o: Option[i32] = Some(7);
    if let Some(v) = o { return v; }
    return -1;
}`},
		{node: "LetElse", src: `function main(): i32 {
    let o: Option[i32] = Some(7);
    let Some(v) = o else { return -1; };
    return v;
}`},
		{node: "While", src: `function main(): i32 {
    let i: i32 = 0;
    while (i < 3) { i = i + 1; }
    return i;
}`},
		{node: "Loop", src: `function main(): i32 {
    let i: i32 = 0;
    loop { i = i + 1; if (i == 3) { break; } }
    return i;
}`},
		{node: "For", src: `function main(): i32 {
    let sum: i32 = 0;
    for (let i: i32 = 0; i < 3; i = i + 1) { sum = sum + i; }
    return sum;
}`},
		{node: "Break", src: `function main(): i32 {
    let i: i32 = 0;
    while (true) { if (i == 3) { break; } i = i + 1; }
    return i;
}`},
		{node: "Continue", src: `function main(): i32 {
    let sum: i32 = 0;
    let i: i32 = 0;
    while (i < 5) { i = i + 1; if (i == 3) { continue; } sum = sum + i; }
    return sum;
}`},
		{node: "Return", src: `function main(): i32 { return 0; }`},
		{node: "Defer", src: `function main(): i32 {
    let n: i32 = 0;
    defer n = n + 100;
    n = 7;
    return n;
}`,
		},
		{node: "Var", src: `function main(): i32 { let x: i32 = 7; return x; }`},
		{node: "Destructure", src: `function main(): i32 { let t: (i32, i32) = (3, 4); let (a, b) = t; return a + b; }`},
		{node: "ExprStmt", src: `function main(): i32 { let n: i32 = 0; n = n + 1; return n; }`},
		{node: "Match", src: `enum Light { Red, Green }
function main(): i32 {
    let l: Light = Green;
    let out: i32 = 0;
    match (l) {
        Red => { out = 1; },
        Green => { out = 2; }
    }
    return out;
}`},
		{node: "FuncDecl", src: `function main(): i32 {
    function bump(x: i32): i32 { return x + 1; }
    return bump(41);
}`},
	}

	for _, c := range cases {
		t.Run(c.node, func(t *testing.T) {
			if c.skip != "" {
				t.Skipf("not implemented in interp: %s", c.skip)
				return
			}
			if c.src == "" {
				t.Fatalf("%s: missing src", c.node)
			}
			prog, _, err := modload.LoadSource(c.src)
			if err != nil {
				t.Fatalf("load: %v\nsrc:\n%s", err, c.src)
			}
			if _, err := checker.Check(prog); err != nil {
				t.Fatalf("check: %v\nsrc:\n%s", err, c.src)
			}
			i := New()
			for _, ed := range prog.Enums {
				i.RegisterEnum(ed)
			}
			for _, fn := range prog.Funcs {
				i.Register(fn)
			}
			if _, err := i.CallByName("main", nil); err != nil {
				t.Fatalf("interp: %v\nsrc:\n%s", err, c.src)
			}
		})
	}

	covered := map[string]bool{}
	for _, c := range cases {
		covered[c.node] = true
	}
	for _, name := range allExprNodeNames() {
		if !covered[name] {
			t.Errorf("missing coverage case for ast.%s (Expr) — add to TestInterpHandlesEveryASTNode's cases slice, or mark Skip with a reason", name)
		}
	}
	for _, name := range allStmtNodeNames() {
		if !covered[name] {
			t.Errorf("missing coverage case for ast.%s (Stmt) — add to TestInterpHandlesEveryASTNode's cases slice, or mark Skip with a reason", name)
		}
	}
}

func allExprNodeNames() []string {
	samples := []ast.Expr{
		(*ast.NumberLit)(nil),
		(*ast.CastExpr)(nil),
		(*ast.BoolLit)(nil),
		(*ast.StringLit)(nil),
		(*ast.FString)(nil),
		(*ast.FloatLit)(nil),
		(*ast.Ident)(nil),
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
	out := make([]string, 0, len(samples))
	for _, s := range samples {
		full := typeName(s)
		if i := strings.LastIndex(full, "."); i >= 0 {
			full = full[i+1:]
		}
		out = append(out, full)
	}
	return out
}

func allStmtNodeNames() []string {
	samples := []ast.Stmt{
		(*ast.Block)(nil),
		(*ast.If)(nil),
		(*ast.While)(nil),
		(*ast.Loop)(nil),
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
	out := make([]string, 0, len(samples))
	for _, s := range samples {
		full := typeName(s)
		if i := strings.LastIndex(full, "."); i >= 0 {
			full = full[i+1:]
		}
		out = append(out, full)
	}
	return out
}

func typeName(v interface{}) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", v), "*")
}
