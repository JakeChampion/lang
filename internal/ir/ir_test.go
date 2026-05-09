package ir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// lowerSource parses, type-checks, and lowers src to IR. The check is
// expected to pass; failures stop the test.
func lowerSource(t *testing.T, src string) *Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// Drop prelude funcs from the AST before lowering — tests
	// in this package assert on user-code shape (indexing into
	// prog.Funcs[0], counting funcs, etc.) and shouldn't have
	// to know about the auto-injected stdlib.
	user := prog.Funcs[:0]
	for _, fn := range prog.Funcs {
		if !fn.IsPrelude {
			user = append(user, fn)
		}
	}
	prog.Funcs = user
	ir, err := Lower(prog, info)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return ir
}

func mustContainOp(t *testing.T, p *Program, fnName string, want OpKind) {
	t.Helper()
	if hasOp(p, fnName, want) {
		return
	}
	t.Errorf("expected %s in %s; ops:\n%s", want, fnName, p)
}

func hasOp(p *Program, fnName string, want OpKind) bool {
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		for _, op := range fn.Ops {
			if op.Kind == want {
				return true
			}
		}
	}
	return false
}

func TestLowerSimpleArithmetic(t *testing.T) {
	p := lowerSource(t, `function f(): i32 { return 1 + 2 * 3; }`)
	if len(p.Funcs) != 1 {
		t.Fatalf("got %d funcs", len(p.Funcs))
	}
	got := p.Funcs[0].Ops
	want := []OpKind{
		OpConstI32, // 1
		OpConstI32, // 2
		OpConstI32, // 3
		OpMul,
		OpAdd,
		OpReturn,
	}
	if len(got) != len(want) {
		t.Fatalf("op count mismatch: got %d, want %d:\n%s", len(got), len(want), p)
	}
	for i, w := range want {
		if got[i].Kind != w {
			t.Errorf("op[%d] = %s, want %s", i, got[i].Kind, w)
		}
	}
}

func TestLowerLocals(t *testing.T) {
	p := lowerSource(t, `function f(): i32 {
		var x: i32 = 5;
		var y: i32 = x + 1;
		return y;
	}`)
	mustContainOp(t, p, "f", OpStoreLocal)
	mustContainOp(t, p, "f", OpLoadLocal)
}

func TestLowerIfElse(t *testing.T) {
	p := lowerSource(t, `function f(n: i32): i32 {
		if (n == 0) { return 1; } else { return 2; }
	}`)
	mustContainOp(t, p, "f", OpIf)
	mustContainOp(t, p, "f", OpElse)
	mustContainOp(t, p, "f", OpEnd)
	mustContainOp(t, p, "f", OpEq)
}

func TestLowerWhileBreakContinue(t *testing.T) {
	p := lowerSource(t, `function f(): i32 {
		var i: i32 = 0;
		while (i < 10) {
			if (i == 5) { break; }
			i = i + 1;
		}
		return i;
	}`)
	// while expands to: outer block + loop, with br_if exiting the
	// block on the inverted condition; `break` becomes an OpBr that
	// targets the outer block at relative depth 1.
	mustContainOp(t, p, "f", OpBlock)
	mustContainOp(t, p, "f", OpLoop)
	mustContainOp(t, p, "f", OpBrIf)
	mustContainOp(t, p, "f", OpBr)
}

func TestLowerForLoopWithStep(t *testing.T) {
	p := lowerSource(t, `function f(): i32 {
		var sum: i32 = 0;
		for (var i: i32 = 0; i < 10; i = i + 1) {
			sum = sum + i;
		}
		return sum;
	}`)
	// for expands to: outer block (break) + loop + inner block
	// (continue) wrapping the body, then step + back-edge.
	mustContainOp(t, p, "f", OpBlock)
	mustContainOp(t, p, "f", OpLoop)
	mustContainOp(t, p, "f", OpBrIf) // condition exit
	mustContainOp(t, p, "f", OpBr)   // back-edge
	mustContainOp(t, p, "f", OpEnd)
}

func TestLowerDirectCall(t *testing.T) {
	p := lowerSource(t, `function add(a: i32, b: i32): i32 { return a + b; }
		function main(): i32 { return add(2, 3); }`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	hasDirect := false
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect && op.Str == "add" && op.I32 == 2 {
			hasDirect = true
		}
	}
	if !hasDirect {
		t.Errorf("expected call $add (argc=2), ops:\n%s", p)
	}
}

func TestLowerIndirectCall(t *testing.T) {
	p := lowerSource(t, `function add(a: i32, b: i32): i32 { return a + b; }
		function apply(f: (i32, i32) => i32, a: i32, b: i32): i32 {
			return f(a, b);
		}`)
	apply := findFunc(p, "apply")
	if apply == nil {
		t.Fatal("apply not found")
	}
	hasIndirect := false
	for _, op := range apply.Ops {
		if op.Kind == OpCallIndirect {
			hasIndirect = true
		}
	}
	if !hasIndirect {
		t.Errorf("expected call_indirect, ops:\n%s", p)
	}
}

func TestLowerShortCircuitAnd(t *testing.T) {
	p := lowerSource(t, `function f(a: boolean, b: boolean): boolean { return a && b; }`)
	// `a && b` lowers to a typed if/else: when a is truthy the body
	// pushes b, otherwise it pushes a normalised 0. Both arms thread
	// an i32 result.
	if !strings.Contains(p.String(), "if i32") {
		t.Errorf("expected `if i32` in lowered output:\n%s", p)
	}
	mustContainOp(t, p, "f", OpElse)
}

func TestLowerFloatArithmetic(t *testing.T) {
	p := lowerSource(t, `function f(): float { return 1.5 + 2.5; }`)
	mustContainOp(t, p, "f", OpFAdd)
	mustContainOp(t, p, "f", OpConstF32)
}

func TestLowerImplicitReturn(t *testing.T) {
	p := lowerSource(t, `function f(): void { var x: i32 = 0; }`)
	last := p.Funcs[0].Ops[len(p.Funcs[0].Ops)-1]
	if last.Kind != OpReturnVoid {
		t.Errorf("expected trailing return_void, got %s", last.Kind)
	}
}

func TestLowerImplicitReturnNumber(t *testing.T) {
	p := lowerSource(t, `function f(): i32 { var x: i32 = 0; }`)
	ops := p.Funcs[0].Ops
	if ops[len(ops)-1].Kind != OpReturn {
		t.Errorf("expected trailing return, got %s", ops[len(ops)-1].Kind)
	}
	if ops[len(ops)-2].Kind != OpConstI32 {
		t.Errorf("expected pad const before return, got %s", ops[len(ops)-2].Kind)
	}
}

func findFunc(p *Program, name string) *Func {
	for _, fn := range p.Funcs {
		if fn.Name == name {
			return fn
		}
	}
	return nil
}

func TestLowerSwitch(t *testing.T) {
	prog := lowerSource(t, `function f(n: i32): i32 {
		switch (n) {
			case 1, 2: return 10;
			case 3: return 30;
			default: return 0;
		}
		return -1;
	}`)
	mustContainOp(t, prog, "f", OpStoreLocal) // tag stash
	mustContainOp(t, prog, "f", OpEq)
	// Each value compares with br_if 0 to the inner block; no-match
	// falls through to a br to the outer per-case block.
	mustContainOp(t, prog, "f", OpBrIf)
	mustContainOp(t, prog, "f", OpBr)
}

func TestLowerTernary(t *testing.T) {
	prog := lowerSource(t, `function f(b: boolean): i32 { return b ? 1 : 2; }`)
	// Ternary lowers to a typed `if i32 ... else ... end`.
	mustContainOp(t, prog, "f", OpIf)
	mustContainOp(t, prog, "f", OpElse)
	mustContainOp(t, prog, "f", OpEnd)
	mustContainOp(t, prog, "f", OpConstI32)
}

func TestLowerArrayLitAndIndex(t *testing.T) {
	prog := lowerSource(t, `function f(): i32 {
		var a: i32[] = [10, 20, 30];
		return a[1];
	}`)
	mustContainOp(t, prog, "f", OpAlloc)
	mustContainOp(t, prog, "f", OpStore) // length prefix + element stores
	// Indexing dispatches via __arr_idx then OpLoad.
	mustContainOp(t, prog, "f", OpCallDirect)
	mustContainOp(t, prog, "f", OpLoad)
}

func TestLowerStringIndex(t *testing.T) {
	prog := lowerSource(t, `function f(): i32 {
		var s: string = "abc";
		return s[1];
	}`)
	mustContainOp(t, prog, "f", OpCallDirect) // __str_idx
	mustContainOp(t, prog, "f", OpLoadByte)
}

func TestLowerStructLitAndFieldAccess(t *testing.T) {
	prog := lowerSource(t, `struct P { x: i32, y: i32 }
		function main(): i32 {
			var p: P = P { x: 10, y: 32 };
			return p.x + p.y;
		}`)
	mustContainOp(t, prog, "main", OpAlloc)
	mustContainOp(t, prog, "main", OpStore)
	mustContainOp(t, prog, "main", OpLoad)
}

// `literal + literal` folds at compile time to a single
// OpConstStr; the runtime OpStrConcat (and the `__lang_strcat`
// it bottoms out in) only fires on at least one non-literal arg.
func TestLowerStringConcatFoldsLiterals(t *testing.T) {
	prog := lowerSource(t, `function f(): string { return "a" + "b"; }`)
	if hasOp(prog, "f", OpStrConcat) {
		t.Errorf("literal + literal must fold; OpStrConcat must not appear:\n%s", prog)
	}
	mustContainOp(t, prog, "f", OpConstStr)
}

func TestLowerStringConcatNonLiteralKeepsRuntime(t *testing.T) {
	prog := lowerSource(t, `function f(s: string): string { return s + "x"; }`)
	mustContainOp(t, prog, "f", OpStrConcat)
}

// `lit == lit` folds at compile time to a const; the runtime
// OpStrEq only fires when at least one side is non-literal.
func TestLowerStringEqualityFoldsLiterals(t *testing.T) {
	prog := lowerSource(t, `function f(): boolean { return "a" == "b"; }`)
	if hasOp(prog, "f", OpStrEq) {
		t.Errorf("lit == lit must fold; OpStrEq must not appear:\n%s", prog)
	}
	mustContainOp(t, prog, "f", OpConstI32)
}

func TestLowerStringEqualityIdentVsLitShortCircuits(t *testing.T) {
	prog := lowerSource(t, `function f(s: string): boolean { return s == "ok"; }`)
	// Length comparison emits a const for the literal length...
	mustContainOp(t, prog, "f", OpConstI32)
	// ...and falls back to OpStrEq only when lengths match.
	mustContainOp(t, prog, "f", OpStrEq)
	// The if/else split is materialised as OpIf in the IR.
	mustContainOp(t, prog, "f", OpIf)
}

// Closure conversion runs as a precondition of Lower, so a nested
// function that captures an outer var should appear in the IR's
// Funcs list with a generated `__closure_*` name and the original
// def site should emit OpMakeClosure.
func TestLowerNestedFunctionHoists(t *testing.T) {
	prog := lowerSource(t, `function outer(): i32 {
		var n: i32 = 7;
		function inner(): i32 { return n + 1; }
		return inner();
	}`)
	// Two functions in the IR: the outer and the hoisted inner.
	if len(prog.Funcs) != 2 {
		t.Fatalf("expected 2 funcs (outer + hoisted inner), got %d:\n%s", len(prog.Funcs), prog)
	}
	var hoisted *Func
	for _, fn := range prog.Funcs {
		if strings.HasPrefix(fn.Name, "__closure_") {
			hoisted = fn
		}
	}
	if hoisted == nil {
		t.Fatalf("expected a hoisted __closure_* function, got:\n%s", prog)
	}
	// The hoisted function carries the synthetic env parameter as its
	// last param so call_indirect through the funcref table is uniform.
	if last := hoisted.Params[len(hoisted.Params)-1].Name; last != "__env" {
		t.Errorf("hoisted function's last param = %q, want __env", last)
	}
	// The outer's def site is now `var inner = MakeClosure{...}`, so
	// outer's ops contain OpMakeClosure.
	mustContainOp(t, prog, "outer", OpMakeClosure)
}

// Captures lower to env-relative loads inside the hoisted body: the
// IR walks `local.get $__env; const offset; add; load`.
func TestLowerCaptureRefIsEnvRelativeLoad(t *testing.T) {
	prog := lowerSource(t, `function outer(): i32 {
		var n: i32 = 5;
		function inner(): i32 { return n; }
		return inner();
	}`)
	var hoisted *Func
	for _, fn := range prog.Funcs {
		if strings.HasPrefix(fn.Name, "__closure_") {
			hoisted = fn
		}
	}
	if hoisted == nil {
		t.Fatalf("hoisted function missing:\n%s", prog)
	}
	// We expect the body to load the env, push the offset, add, then
	// load the captured word — so OpAdd + OpLoad must both appear.
	mustContainOp(t, prog, hoisted.Name, OpAdd)
	mustContainOp(t, prog, hoisted.Name, OpLoad)
	// And the env parameter must be referenced via OpLoadLocal at its
	// param-slot index (the last param's slot).
	envSlot := int32(len(hoisted.Params) - 1)
	hasEnvLoad := false
	for _, op := range hoisted.Ops {
		if op.Kind == OpLoadLocal && op.I32 == envSlot {
			hasEnvLoad = true
		}
	}
	if !hasEnvLoad {
		t.Errorf("hoisted body never loads the __env slot %d:\n%s", envSlot, prog)
	}
}

// Every function's op list must be a balanced sequence of structured
// scopes: each Block/Loop/If is matched by an End, and the depth is
// 0 at function entry and exit. This is the precondition any
// structured-control-flow target (WAT, etc.) relies on.
func TestStructuredControlFlowIsBalanced(t *testing.T) {
	prog := lowerSource(t, `function f(n: i32): i32 {
		var sum: i32 = 0;
		for (var i: i32 = 0; i < n; i = i + 1) {
			if (i == 5) { break; }
			if (i == 7) { continue; }
			switch (i) {
				case 1, 2: sum = sum + 10;
				case 3: sum = sum + 30;
				default: sum = sum + 1;
			}
		}
		while (sum > 100) {
			sum = sum - 1;
		}
		return sum > 0 ? sum : 0;
	}`)
	for _, fn := range prog.Funcs {
		depth := 0
		for i, op := range fn.Ops {
			switch op.Kind {
			case OpBlock, OpLoop, OpIf:
				depth++
			case OpEnd:
				depth--
				if depth < 0 {
					t.Fatalf("%s: op %d (%s): depth went negative", fn.Name, i, op.Kind)
				}
			case OpBr, OpBrIf:
				if op.I32 < 0 || op.I32 > int32(depth-1) {
					t.Errorf("%s: op %d (%s %d): branch depth out of range (depth=%d)",
						fn.Name, i, op.Kind, op.I32, depth)
				}
			}
		}
		if depth != 0 {
			t.Errorf("%s: ended at depth %d, want 0", fn.Name, depth)
		}
	}
}

// ScratchTypes records the type of every synthetic slot the lowering pass
// conjured beyond the user-visible params + locals — codegen needs
// the count to declare matching WAT locals.
func TestLowerNumScratchTracked(t *testing.T) {
	// A program with no synthetic helpers: ScratchTypes is empty.
	pPlain := lowerSource(t, `function f(a: i32, b: i32): i32 {
		var x: i32 = a + b;
		return x;
	}`)
	if got := len(pPlain.Funcs[0].ScratchTypes); got != 0 {
		t.Errorf("plain function: len(ScratchTypes) = %d, want 0", got)
	}
	// A program using array, struct, and switch helpers should report
	// at least one scratch slot per helper kind.
	pHelpers := lowerSource(t, `struct P { x: i32 }
		function f(n: i32): i32 {
			var a: i32[] = [1, 2, 3];
			var p: P = P { x: 5 };
			switch (n) { case 0: return 0; default: return 1; }
		}`)
	if got := len(pHelpers.Funcs[0].ScratchTypes); got < 3 {
		t.Errorf("helper-heavy function: len(ScratchTypes) = %d, want >= 3", got)
	}
}

// OpCallIndirect carries the static signature of the function-typed
// local so codegen can resolve a `(type $tN)` clause without tracing
// back through the preceding OpLoadLocal.
func TestLowerCallIndirectCarriesSig(t *testing.T) {
	prog := lowerSource(t, `function add(a: i32, b: i32): i32 { return a + b; }
		function apply(f: (i32, i32) => i32, a: i32, b: i32): i32 {
			return f(a, b);
		}`)
	apply := findFunc(prog, "apply")
	if apply == nil {
		t.Fatal("apply not found")
	}
	for _, op := range apply.Ops {
		if op.Kind != OpCallIndirect {
			continue
		}
		if op.Sig == nil {
			t.Fatalf("OpCallIndirect.Sig is nil:\n%s", prog)
		}
		if len(op.Sig.Params) != 2 {
			t.Errorf("Sig.Params = %d, want 2", len(op.Sig.Params))
		}
		return
	}
	t.Fatalf("OpCallIndirect not found:\n%s", prog)
}

// Lowering stamps each op with the source position of the AST node
// it came from, so consumers can emit per-statement DWARF .loc /
// debug-line entries. The recorded line tracks the surface syntax
// (e.g. operands inside an expression statement carry the line of
// that expression).
func TestLowerStampsSourcePositions(t *testing.T) {
	src := `function f(a: i32, b: i32): i32 {
		var x: i32 = a + b;
		return x;
	}`
	prog := lowerSource(t, src)
	if len(prog.Funcs) != 1 {
		t.Fatalf("got %d funcs", len(prog.Funcs))
	}
	fn := prog.Funcs[0]
	// First op evaluates `a` (line 2 in the source). Last meaningful
	// op is the OpReturn from `return x;` on line 3.
	if got := fn.Ops[0].Pos.Line; got != 2 {
		t.Errorf("first op (load `a`) at line %d, want 2", got)
	}
	var lastReturn int = -1
	for i, op := range fn.Ops {
		if op.Kind == OpReturn {
			lastReturn = i
		}
	}
	if lastReturn < 0 {
		t.Fatalf("no OpReturn found:\n%s", prog)
	}
	if got := fn.Ops[lastReturn].Pos.Line; got != 3 {
		t.Errorf("OpReturn at line %d, want 3", got)
	}
}

// MakeClosure carries the hoisted function name and a capture count
// so codegen can resolve both the funcref-table index and the env
// block size.
func TestLowerMakeClosureCarriesNameAndCount(t *testing.T) {
	prog := lowerSource(t, `function outer(): i32 {
		var a: i32 = 1;
		var b: i32 = 2;
		function inner(): i32 { return a + b; }
		return inner();
	}`)
	outer := findFunc(prog, "outer")
	if outer == nil {
		t.Fatal("outer not found")
	}
	var mc *Op
	for i := range outer.Ops {
		if outer.Ops[i].Kind == OpMakeClosure {
			mc = &outer.Ops[i]
		}
	}
	if mc == nil {
		t.Fatalf("OpMakeClosure missing in outer:\n%s", prog)
	}
	if !strings.HasPrefix(mc.Str, "__closure_inner_") {
		t.Errorf("MakeClosure name = %q, want __closure_inner_*", mc.Str)
	}
	if mc.I32 != 2 {
		t.Errorf("MakeClosure capture count = %d, want 2", mc.I32)
	}
}

// Arithmetic / bitwise identities at the IR builder elide the
// trivial side. Tests use a parameter for the non-literal side
// so the all-const fold path doesn't preempt.
func TestLowerArithIdentities(t *testing.T) {
	cases := []struct {
		src      string
		bannedOp OpKind // op that the fold should remove
		desc     string
	}{
		{`function f(x: i32): i32 { return x + 0; }`, OpAdd, "x + 0"},
		{`function f(x: i32): i32 { return 0 + x; }`, OpAdd, "0 + x"},
		{`function f(x: i32): i32 { return x - 0; }`, OpSub, "x - 0"},
		{`function f(x: i32): i32 { return x * 1; }`, OpMul, "x * 1"},
		{`function f(x: i32): i32 { return 1 * x; }`, OpMul, "1 * x"},
		{`function f(x: i32): i32 { return x | 0; }`, OpOr, "x | 0"},
		{`function f(x: i32): i32 { return x ^ 0; }`, OpXor, "x ^ 0"},
		{`function f(x: i32): i32 { return x & -1; }`, OpAnd, "x & -1"},
		{`function f(x: i32): i32 { return x << 0; }`, OpShl, "x << 0"},
		{`function f(x: i32): i32 { return x >> 0; }`, OpShrS, "x >> 0"},
	}
	for _, tc := range cases {
		prog := lowerSource(t, tc.src)
		if hasOp(prog, "f", tc.bannedOp) {
			t.Errorf("%s should fold; %s must not appear:\n%s", tc.desc, tc.bannedOp, prog)
		}
	}
}

// Self-identity folds collapse `x op x` to a known constant
// or the operand itself. Inspired by Cranelift's icmp.isle /
// arithmetic.isle. Restricted to plain identifiers so we
// don't double-evaluate side effects.
func TestLowerSelfIdentityFolds(t *testing.T) {
	cases := []struct {
		op       string
		bannedOp OpKind
		desc     string
	}{
		{"-", OpSub, "x - x"},
		{"^", OpXor, "x ^ x"},
		{"|", OpOr, "x | x"},
		{"&", OpAnd, "x & x"},
		{"==", OpEq, "x == x"},
		{"!=", OpNe, "x != x"},
		{"<", OpLtS, "x < x"},
		{"<=", OpLeS, "x <= x"},
		{">", OpGtS, "x > x"},
		{">=", OpGeS, "x >= x"},
	}
	for _, tc := range cases {
		retType := "i32"
		if tc.op == "==" || tc.op == "!=" || tc.op == "<" ||
			tc.op == "<=" || tc.op == ">" || tc.op == ">=" {
			retType = "boolean"
		}
		src := "function f(x: i32): " + retType + " { return x " + tc.op + " x; }"
		prog := lowerSource(t, src)
		if hasOp(prog, "f", tc.bannedOp) {
			t.Errorf("%s should fold; %s must not appear:\n%s", tc.desc, tc.bannedOp, prog)
		}
	}
}

// `x * 2^k` strength-reduces to `x << k` (k > 0). For
// non-power-of-two multipliers the OpMul stays.
func TestLowerArithStrengthReducesMulPow2(t *testing.T) {
	cases := []struct {
		src      string
		wantOp   OpKind
		bannedOp OpKind
		desc     string
	}{
		{`function f(x: i32): i32 { return x * 2; }`, OpShl, OpMul, "x * 2"},
		{`function f(x: i32): i32 { return x * 4; }`, OpShl, OpMul, "x * 4"},
		{`function f(x: i32): i32 { return x * 16; }`, OpShl, OpMul, "x * 16"},
		{`function f(x: i32): i32 { return 8 * x; }`, OpShl, OpMul, "8 * x"},
		{`function f(x: i32): i32 { return x * 3; }`, OpMul, OpShl, "x * 3"},
		{`function f(x: i32): i32 { return x * 7; }`, OpMul, OpShl, "x * 7"},
	}
	for _, tc := range cases {
		prog := lowerSource(t, tc.src)
		if hasOp(prog, "f", tc.bannedOp) {
			t.Errorf("%s: %s should not appear:\n%s", tc.desc, tc.bannedOp, prog)
		}
		if !hasOp(prog, "f", tc.wantOp) {
			t.Errorf("%s: expected %s in lowered IR:\n%s", tc.desc, tc.wantOp, prog)
		}
	}
}

