package closureconv_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/closureconv"
	"github.com/jakechampion/lang/internal/parser"
)

// runConvertWith parses + type-checks `src`, then runs the
// pointer-width-aware ConvertWith entry point. Returns the
// mutated program and the checker.Info (so tests can assert on
// the hoisted-function signatures registered in FuncSigs).
// closureconv mutates prog in place.
func runConvertWith(t *testing.T, src string, ptrW int) (*ast.Program, *checker.Info) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := closureconv.ConvertWith(prog, info, ptrW); err != nil {
		t.Fatalf("convert(ptrW=%d): %v", ptrW, err)
	}
	return prog, info
}

// deepWalkBlock is a fuller AST expression walker than the
// closureconv_test.go `walkExpr` helper: it additionally
// recurses through CastExpr, Index and FieldAccess so capture
// references buried under `as`-casts / indexing / field access
// (the shapes these deep tests use) are reached.
func deepWalkBlock(blk *ast.Block, visit func(ast.Expr)) {
	if blk == nil {
		return
	}
	for _, s := range blk.Stmts {
		deepWalkStmt(s, visit)
	}
}

func deepWalkStmt(s ast.Stmt, visit func(ast.Expr)) {
	switch x := s.(type) {
	case *ast.Var:
		deepWalkInExpr(x.Init, visit)
	case *ast.ExprStmt:
		deepWalkInExpr(x.Expr, visit)
	case *ast.Return:
		deepWalkInExpr(x.Value, visit)
	case *ast.Block:
		deepWalkBlock(x, visit)
	case *ast.If:
		deepWalkInExpr(x.Cond, visit)
		deepWalkStmt(x.Then, visit)
		if x.Else != nil {
			deepWalkStmt(x.Else, visit)
		}
	}
}

func deepWalkInExpr(e ast.Expr, visit func(ast.Expr)) {
	if e == nil {
		return
	}
	visit(e)
	switch x := e.(type) {
	case *ast.Binary:
		deepWalkInExpr(x.Left, visit)
		deepWalkInExpr(x.Right, visit)
	case *ast.Unary:
		deepWalkInExpr(x.Operand, visit)
	case *ast.CastExpr:
		deepWalkInExpr(x.Inner, visit)
	case *ast.Call:
		deepWalkInExpr(x.Callee, visit)
		for _, a := range x.Args {
			deepWalkInExpr(a, visit)
		}
	case *ast.Index:
		deepWalkInExpr(x.Array, visit)
		deepWalkInExpr(x.Idx, visit)
	case *ast.FieldAccess:
		deepWalkInExpr(x.Target, visit)
	}
}

// captureOffsets walks a hoisted function body and returns a
// name->offset map built from every CaptureRef it finds. Each
// captured name appears once in these programs.
func captureOffsets(blk *ast.Block) map[string]int {
	out := map[string]int{}
	deepWalkBlock(blk, func(e ast.Expr) {
		if cr, ok := e.(*ast.CaptureRef); ok {
			out[cr.Name] = cr.Offset
		}
	})
	return out
}

// hoistedByPrefix returns the first hoisted FuncDecl in prog
// whose name starts with `__closure_<orig>_`.
func hoistedByPrefix(prog *ast.Program, orig string) *ast.FuncDecl {
	pfx := "__closure_" + orig + "_"
	for _, fn := range prog.Funcs {
		if strings.HasPrefix(fn.Name, pfx) {
			return fn
		}
	}
	return nil
}

// TestConvertMultiCaptureScalarOffsets — a closure capturing
// three scalars in mixed widths (i32, i64, i32) lays them out
// sequentially in the env block with width-sized slots: i32
// slots are 4 bytes, i64 slots are 8. Confirmed layout:
// a(i32)@0, b(i64)@4, c(i32)@12. The offsets are independent of
// ptrW because none of the captures is pointer-shaped.
func TestConvertMultiCaptureScalarOffsets(t *testing.T) {
	src := `function main(): i32 {
		var a: i32 = 1;
		var b: i64 = 2;
		var c: i32 = 3;
		function f(x: i32): i64 { return (x as i64) + (a as i64) + b + (c as i64); }
		return f(0) as i32;
	}`
	for _, ptrW := range []int{4, 8} {
		prog, _ := runConvertWith(t, src, ptrW)
		clone := hoistedByPrefix(prog, "f")
		if clone == nil {
			t.Fatalf("ptrW=%d: hoisted `f` clone not found", ptrW)
		}
		offs := captureOffsets(clone.Body)
		want := map[string]int{"a": 0, "b": 4, "c": 12}
		for name, w := range want {
			if got, ok := offs[name]; !ok {
				t.Errorf("ptrW=%d: capture %q not rewritten to CaptureRef", ptrW, name)
			} else if got != w {
				t.Errorf("ptrW=%d: capture %q offset = %d, want %d", ptrW, name, got, w)
			}
		}
		if len(offs) != 3 {
			t.Errorf("ptrW=%d: expected 3 distinct captures, got %d (%v)", ptrW, len(offs), offs)
		}
	}
}

// TestConvertPtrWidthArrayCaptureStride — an array capture
// (pointer-shaped) takes `ptrW` bytes in the env block, so the
// offset of a following scalar capture differs between targets:
// at ptrW=4 the trailing i32 sits at offset 4, at ptrW=8 it
// sits at offset 8. The array capture itself is always at
// offset 0. This is the clean ptrW-sensitivity case.
func TestConvertPtrWidthArrayCaptureStride(t *testing.T) {
	src := `function main(): i32 {
		var arr: i32[] = [1, 2, 3];
		var n: i32 = 9;
		function f(): i32 { return arr[0] + n; }
		return f();
	}`
	cases := []struct {
		ptrW  int
		wantN int
	}{
		{4, 4},
		{8, 8},
	}
	for _, tc := range cases {
		prog, _ := runConvertWith(t, src, tc.ptrW)
		clone := hoistedByPrefix(prog, "f")
		if clone == nil {
			t.Fatalf("ptrW=%d: hoisted `f` clone not found", tc.ptrW)
		}
		offs := captureOffsets(clone.Body)
		if got := offs["arr"]; got != 0 {
			t.Errorf("ptrW=%d: arr capture offset = %d, want 0", tc.ptrW, got)
		}
		if got := offs["n"]; got != tc.wantN {
			t.Errorf("ptrW=%d: n capture offset = %d, want %d (array slot is ptrW=%d bytes)",
				tc.ptrW, got, tc.wantN, tc.ptrW)
		}
		// Sanity: the array capture's recorded static type is the
		// array type, not the scalar.
		var arrType ast.Type
		deepWalkBlock(clone.Body, func(e ast.Expr) {
			if cr, ok := e.(*ast.CaptureRef); ok && cr.Name == "arr" {
				arrType = cr.Type
			}
		})
		if _, ok := arrType.(ast.ArrayType); !ok {
			t.Errorf("ptrW=%d: arr CaptureRef.Type = %T, want ast.ArrayType", tc.ptrW, arrType)
		}
	}
}

// TestConvertStringCaptureSlot — a string capture occupies
// a two-word `(data, len)` slot. At ptrW=4 that's 2*4=8 bytes; at
// ptrW=8 the string is treated as a single pointer-shaped 8-byte
// slot (TwoWordOverride defaults off off-wasm). Both land a
// trailing i32 capture at offset 8, so unlike the array case the
// string slot size does NOT diverge across these two widths — a
// finding worth pinning so a future TwoWordOverride flip is
// caught here.
func TestConvertStringCaptureSlot(t *testing.T) {
	src := `function main(): i32 {
		var s: string = "hi";
		var n: i32 = 5;
		function f(): i32 { return s.len() + n; }
		return f();
	}`
	for _, ptrW := range []int{4, 8} {
		prog, _ := runConvertWith(t, src, ptrW)
		clone := hoistedByPrefix(prog, "f")
		if clone == nil {
			t.Fatalf("ptrW=%d: hoisted `f` clone not found", ptrW)
		}
		offs := captureOffsets(clone.Body)
		if got := offs["s"]; got != 0 {
			t.Errorf("ptrW=%d: s capture offset = %d, want 0", ptrW, got)
		}
		if got := offs["n"]; got != 8 {
			t.Errorf("ptrW=%d: n capture offset = %d, want 8 (string slot is 8 bytes)", ptrW, got)
		}
		var sType ast.Type
		deepWalkBlock(clone.Body, func(e ast.Expr) {
			if cr, ok := e.(*ast.CaptureRef); ok && cr.Name == "s" {
				sType = cr.Type
			}
		})
		if _, ok := sType.(ast.StringType); !ok {
			t.Errorf("ptrW=%d: s CaptureRef.Type = %T, want ast.StringType", ptrW, sType)
		}
	}
}

// TestConvertZeroCaptureClosure — a nested function that captures
// nothing still hoists to a top-level decl and the def site is
// replaced with a MakeClosure carrying an empty Captures list.
// The hoisted func still gets the trailing synthetic `__env`
// param even though no env slots are populated.
func TestConvertZeroCaptureClosure(t *testing.T) {
	src := `function main(): i32 {
		function f(x: i32): i32 { return x + 1; }
		return f(3);
	}`
	prog, _ := runConvertWith(t, src, 8)
	clone := hoistedByPrefix(prog, "f")
	if clone == nil {
		t.Fatal("hoisted `f` clone not found")
	}
	if len(clone.Params) == 0 || clone.Params[len(clone.Params)-1].Name != "__env" {
		t.Errorf("hoisted f should end with synthetic __env param, got params %+v", clone.Params)
	}
	// No CaptureRef should appear in a zero-capture body.
	deepWalkBlock(clone.Body, func(e ast.Expr) {
		if cr, ok := e.(*ast.CaptureRef); ok {
			t.Errorf("zero-capture body unexpectedly has CaptureRef %q", cr.Name)
		}
	})
	mainFn := findFuncByName(prog, "main")
	v := findVarStmt(mainFn.Body, "f")
	if v == nil {
		t.Fatal("def site `var f = ...` not found")
	}
	mc, ok := v.Init.(*ast.MakeClosure)
	if !ok {
		t.Fatalf("f init: expected *ast.MakeClosure, got %T", v.Init)
	}
	if len(mc.Captures) != 0 {
		t.Errorf("zero-capture MakeClosure should have empty Captures, got %d", len(mc.Captures))
	}
	if mc.FuncName != clone.Name {
		t.Errorf("MakeClosure.FuncName = %q, want hoisted name %q", mc.FuncName, clone.Name)
	}
	if mc.FuncIndex < 0 || mc.FuncIndex >= len(prog.Funcs) || prog.Funcs[mc.FuncIndex] != clone {
		t.Errorf("MakeClosure.FuncIndex=%d does not point at the hoisted clone in prog.Funcs", mc.FuncIndex)
	}
}

// TestConvertNestedClosureCapturesBothScopes — a closure nested
// inside another local function (`inner` inside `outer`) captures
// a variable from main (`a`) AND one from `outer` (`b`). Both are
// rewritten to CaptureRef in the innermost hoisted body, laid out
// at offsets 0 and 4. Both `outer` and `inner` hoist to top-level
// decls, each gaining its own `__env` param.
func TestConvertNestedClosureCapturesBothScopes(t *testing.T) {
	src := `function main(): i32 {
		var a: i32 = 1;
		function outer(): i32 {
			var b: i32 = 2;
			function inner(): i32 { return a + b; }
			return inner();
		}
		return outer();
	}`
	prog, _ := runConvertWith(t, src, 8)
	outerClone := hoistedByPrefix(prog, "outer")
	innerClone := hoistedByPrefix(prog, "inner")
	if outerClone == nil || innerClone == nil {
		t.Fatalf("expected both outer (%v) and inner (%v) hoisted", outerClone != nil, innerClone != nil)
	}
	if outerClone.Params[len(outerClone.Params)-1].Name != "__env" {
		t.Error("outer clone missing trailing __env param")
	}
	if innerClone.Params[len(innerClone.Params)-1].Name != "__env" {
		t.Error("inner clone missing trailing __env param")
	}
	offs := captureOffsets(innerClone.Body)
	if got, ok := offs["a"]; !ok || got != 0 {
		t.Errorf("inner capture a: ok=%v offset=%d, want offset 0", ok, got)
	}
	if got, ok := offs["b"]; !ok || got != 4 {
		t.Errorf("inner capture b: ok=%v offset=%d, want offset 4", ok, got)
	}
	if len(offs) != 2 {
		t.Errorf("inner should capture exactly a and b, got %v", offs)
	}
}

// TestConvertTopLevelRefNotRewritten — a free reference to a
// top-level function (`helper`) inside a hoisted body stays an
// *ast.Ident; only the genuine outer-local capture (`n`) becomes
// a CaptureRef. The closure's own parameter (`x`) also stays an
// Ident. Guards against the converter over-eagerly rewriting
// every name to a CaptureRef.
func TestConvertTopLevelRefNotRewritten(t *testing.T) {
	src := `function helper(x: i32): i32 { return x * 2; }
function main(): i32 {
	var n: i32 = 5;
	function f(x: i32): i32 { return helper(x) + n; }
	return f(3);
}`
	prog, _ := runConvertWith(t, src, 8)
	clone := hoistedByPrefix(prog, "f")
	if clone == nil {
		t.Fatal("hoisted `f` clone not found")
	}
	var sawHelperIdent, sawParamIdent, sawNCapture bool
	deepWalkBlock(clone.Body, func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok {
			switch id.Name {
			case "helper":
				sawHelperIdent = true
			case "x":
				sawParamIdent = true
			case "n":
				t.Error("outer-local `n` left as Ident; should be CaptureRef")
			}
		}
		if cr, ok := e.(*ast.CaptureRef); ok {
			switch cr.Name {
			case "n":
				sawNCapture = true
			case "helper", "x":
				t.Errorf("%q should not be a CaptureRef", cr.Name)
			}
		}
	})
	if !sawHelperIdent {
		t.Error("expected top-level `helper` to remain an *ast.Ident")
	}
	if !sawParamIdent {
		t.Error("expected closure param `x` to remain an *ast.Ident")
	}
	if !sawNCapture {
		t.Error("expected outer-local `n` rewritten to *ast.CaptureRef")
	}
}

// TestConvertHoistedSigHasEnvParam — closureconv registers a
// FuncSig for the hoisted name whose final parameter is the
// synthetic env pointer (ast.NumberType), so indirect-call
// codegen can resolve the env-carrying signature. The original
// local-name signature is unaffected (the outer body's calls
// keep binding against it).
func TestConvertHoistedSigHasEnvParam(t *testing.T) {
	src := `function main(): i32 {
		var k: i32 = 7;
		function f(x: i32): i32 { return x + k; }
		return f(1);
	}`
	prog, info := runConvertWith(t, src, 8)
	clone := hoistedByPrefix(prog, "f")
	if clone == nil {
		t.Fatal("hoisted `f` clone not found")
	}
	sig, ok := info.FuncSigs[clone.Name]
	if !ok {
		t.Fatalf("no FuncSig registered for hoisted name %q", clone.Name)
	}
	if len(sig.Params) != 2 {
		t.Fatalf("hoisted sig should have 2 params (x, __env), got %d", len(sig.Params))
	}
	if _, ok := sig.Params[len(sig.Params)-1].(ast.NumberType); !ok {
		t.Errorf("hoisted sig last param = %T, want ast.NumberType (env pointer)", sig.Params[len(sig.Params)-1])
	}
}

// TestConvertMakeClosureCapturesAreOuterNames — the MakeClosure
// at the def site lists one capture expression per captured
// variable, each being the outer-scope value source. For a
// top-level def site (no enclosing closure) these are plain
// *ast.Ident nodes naming the captured locals, in capture order.
func TestConvertMakeClosureCapturesAreOuterNames(t *testing.T) {
	src := `function main(): i32 {
		var p: i32 = 1;
		var q: i32 = 2;
		function f(): i32 { return p + q; }
		return f();
	}`
	prog, _ := runConvertWith(t, src, 8)
	mainFn := findFuncByName(prog, "main")
	v := findVarStmt(mainFn.Body, "f")
	if v == nil {
		t.Fatal("def site `var f = ...` not found")
	}
	mc, ok := v.Init.(*ast.MakeClosure)
	if !ok {
		t.Fatalf("f init: expected *ast.MakeClosure, got %T", v.Init)
	}
	if len(mc.Captures) != 2 {
		t.Fatalf("expected 2 capture exprs, got %d", len(mc.Captures))
	}
	got := make([]string, 0, 2)
	for _, ce := range mc.Captures {
		id, ok := ce.(*ast.Ident)
		if !ok {
			t.Fatalf("capture expr = %T, want *ast.Ident at top-level def site", ce)
		}
		got = append(got, id.Name)
	}
	if got[0] != "p" || got[1] != "q" {
		t.Errorf("capture order = %v, want [p q]", got)
	}
}
