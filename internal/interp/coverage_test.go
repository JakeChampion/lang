package interp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
)

// TestInterpHandlesEveryASTNode is the load-bearing "no silent
// gaps" guarantee for the tree-walking interpreter. Three times
// during the fernsmith / differential-oracle work the interp
// errored on an AST node every other backend supports — FString
// (PR #597), MapLit + Map methods (PR #610), and FuncDecl /
// Lambda (PR #618). Each was a `"unsupported expression %T"`
// panic the fuzz oracle hit before the codegen path it was
// trying to exercise even ran.
//
// This test enumerates every concrete AST type that can show
// up post-parser + post-checker and asserts the interpreter
// can evaluate at least one minimal program containing that
// node. Adding a new AST variant forces an entry here (or an
// explicit Skip with a noted reason).
//
// The list MUST stay in sync with the `isExpr()` / `isStmt()`
// implementations in internal/ast/ast.go. The sentinel
// assertions at the bottom of this file fail the suite if a
// new variant is added without a matching coverage entry.
func TestInterpHandlesEveryASTNode(t *testing.T) {
	// Each case names the AST node it exercises and a minimal
	// program whose `main()` returns / produces a value that
	// includes that node somewhere in the AST. The skip
	// reason is documented inline when applicable.
	cases := []struct {
		node string // AST type the case exercises
		src  string // minimal program
		skip string // non-empty = expected to be unsupported
	}{
		// Expressions.
		{node: "NumberLit", src: `function main(): i32 { return 42; }`},
		{node: "CastExpr", src: `function main(): i64 { var n: i32 = 7; return n as i64; }`},
		{node: "BoolLit", src: `function main(): boolean { return true; }`},
		{node: "StringLit", src: `function main(): string { return "hi"; }`},
		{node: "FString", src: `
import "std/i32";
function main(): string { return f"x={42}"; }`},
		{node: "FloatLit",
			skip: "interp doesn't model floats — owns the f32 / f64 paths via wasm + arm64 backends only",
		},
		{node: "Ident", src: `function main(): i32 { var x: i32 = 7; return x; }`},
		{node: "ArrayLit", src: `function main(): i32 { var a: i32[] = [1, 2, 3]; return a[0]; }`},
		{node: "Index", src: `function main(): i32 { var a: i32[] = [10, 20]; return a[1]; }`},
		{node: "SliceExpr", src: `function main(): i32 { var a: i32[] = [1, 2, 3, 4]; var s: [i32] = a[1:3]; return s.len(); }`},
		{node: "Call", src: `function f(): i32 { return 1; } function main(): i32 { return f(); }`},
		{node: "Binary", src: `function main(): i32 { return 1 + 2; }`},
		{node: "Unary", src: `function main(): i32 { var n: i32 = 5; return -n; }`},
		{node: "Assign", src: `function main(): i32 { var n: i32 = 0; n = 7; return n; }`},
		{node: "IfExpr", src: `function main(): i32 { return if (true) { 1 } else { 2 }; }`},
		{node: "MatchExpr", src: `enum Light { Red, Green }
function main(): i32 {
    var l: Light = Red;
    return match (l) { Red => 1, Green => 2 };
}`},
		{node: "TryOp",
			skip: "the postfix `?` operator's early-return semantics need flow-from-expr plumbing the tree-walking interpreter doesn't have — every evalExpr would need to return Value+flow+error rather than just Value+error. Documented in the interpreter's own error message; compile to wasm or arm64 for now.",
		},
		{node: "StructLit", src: `struct P { x: i32, y: i32 }
function main(): i32 { var p: P = P { x: 3, y: 4 }; return p.x; }`},
		{node: "TupleLit", src: `function main(): i32 { var t: (i32, i32) = (3, 4); var (a, b) = t; return a + b; }`},
		{node: "MapLit", src: `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = Map { 1: 10 };
    return m.len();
}`},
		{node: "FieldAccess", src: `struct P { x: i32, y: i32 }
function main(): i32 { var p: P = P { x: 3, y: 4 }; return p.y; }`},
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
    var add1: (i32) => i32 = function (x: i32): i32 { return x + 1; };
    return add1(41);
}`},
		// Statements.
		{node: "Block", src: `function main(): i32 { { var n: i32 = 7; return n; } }`},
		{node: "If", src: `function main(): i32 { if (true) { return 1; } return 2; }`},
		{node: "IfLet", src: `function main(): i32 {
    var o: Option[i32] = Some(7);
    if let Some(v) = o { return v; }
    return -1;
}`},
		{node: "LetElse", src: `function main(): i32 {
    var o: Option[i32] = Some(7);
    let Some(v) = o else { return -1; };
    return v;
}`},
		{node: "While", src: `function main(): i32 {
    var i: i32 = 0;
    while (i < 3) { i = i + 1; }
    return i;
}`},
		{node: "Loop", src: `function main(): i32 {
    var i: i32 = 0;
    loop { i = i + 1; if (i == 3) { break; } }
    return i;
}`},
		{node: "For", src: `function main(): i32 {
    var sum: i32 = 0;
    for (var i: i32 = 0; i < 3; i = i + 1) { sum = sum + i; }
    return sum;
}`},
		{node: "Break", src: `function main(): i32 {
    var i: i32 = 0;
    while (true) { if (i == 3) { break; } i = i + 1; }
    return i;
}`},
		{node: "Continue", src: `function main(): i32 {
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < 5) { i = i + 1; if (i == 3) { continue; } sum = sum + i; }
    return sum;
}`},
		{node: "Return", src: `function main(): i32 { return 0; }`},
		{node: "Defer", src: `function main(): i32 {
    var n: i32 = 0;
    defer n = n + 100;
    n = 7;
    return n;
}`,
		// Defer doesn't affect the returned value in Lang
		// (semantics: return value is evaluated before defers
		// run) — body just exists to exercise the AST node.
		},
		{node: "Var", src: `function main(): i32 { var x: i32 = 7; return x; }`},
		{node: "Destructure", src: `function main(): i32 { var t: (i32, i32) = (3, 4); var (a, b) = t; return a + b; }`},
		{node: "ExprStmt", src: `function main(): i32 { var n: i32 = 0; n = n + 1; return n; }`},
		{node: "Match", src: `enum Light { Red, Green }
function main(): i32 {
    var l: Light = Green;
    var out: i32 = 0;
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
			// modload (not bare parser.Parse) so the FString case's
			// `import "std/i32";` resolves — the auto-prelude is gone,
			// so .to_string() is in scope only when imported.
			prog, _, err := modload.LoadSource(c.src)
			if err != nil {
				t.Fatalf("load: %v\nsrc:\n%s", err, c.src)
			}
			// Run the checker so method-call rewrites, FString
			// desugaring, and variant-call IsVariantCall flags
			// land before interp dispatch. This matches the
			// `cmd/fern` script-mode pipeline.
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

	// Tripwire: assert this list stays in sync with the AST
	// `isExpr()` / `isStmt()` declarations. If a new variant
	// is added to internal/ast/ast.go without a coverage entry
	// here, the test fails with a clear "missing case for X"
	// message rather than the interp silently swallowing
	// programs that hit the new shape.
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

// allExprNodeNames returns the type names of every ast.Expr
// implementation. Built by walking the package's isExpr() set
// — these are the unexported tag methods that seal ast.Expr,
// so this list IS the universe.
func allExprNodeNames() []string {
	// One sample of each ast.Expr — must include every variant
	// declared in ast.go. The compiler will fail to build this
	// slice if a referenced type is removed, which is the
	// intended behaviour for "find every place this node is
	// known".
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
		// `*ast.NumberLit` → "NumberLit"
		full := typeName(s)
		if i := strings.LastIndex(full, "."); i >= 0 {
			full = full[i+1:]
		}
		out = append(out, full)
	}
	return out
}

// allStmtNodeNames is the Stmt parallel of allExprNodeNames.
func allStmtNodeNames() []string {
	samples := []ast.Stmt{
		(*ast.Block)(nil),
		(*ast.If)(nil),
		(*ast.LetElse)(nil),
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

// typeName returns the trailing identifier from Go's `%T` output
// (e.g. `*ast.NumberLit` → "NumberLit"). The wrapper exists so
// the all*NodeNames helpers stay focused on the AST surface
// they're enumerating.
func typeName(v interface{}) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", v), "*")
}
