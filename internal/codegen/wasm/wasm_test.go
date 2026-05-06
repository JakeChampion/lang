package wasm

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

func compileToWAT(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	wat, err := Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return wat
}

func mustContain(t *testing.T, wat, needle string) {
	t.Helper()
	if !strings.Contains(wat, needle) {
		t.Errorf("expected output to contain %q\n--- output ---\n%s", needle, wat)
	}
}

func TestSimpleFunction(t *testing.T) {
	wat := compileToWAT(t, `function main(): number { return 42; }`)
	mustContain(t, wat, "(module")
	mustContain(t, wat, `(func $main (result i32)`)
	mustContain(t, wat, "i32.const 42")
	mustContain(t, wat, `(export "main" (func $main))`)
}

func TestArithmeticOps(t *testing.T) {
	wat := compileToWAT(t, `function main(): number { return 7 % 3 + (12 & 10); }`)
	mustContain(t, wat, "i32.rem_s")
	mustContain(t, wat, "i32.and")
	mustContain(t, wat, "i32.add")
}

func TestComparisonReturnsI32(t *testing.T) {
	wat := compileToWAT(t, `function f(): boolean { return 1 < 2; }`)
	mustContain(t, wat, "i32.lt_s")
	mustContain(t, wat, "(result i32)")
}

func TestShortCircuitAnd(t *testing.T) {
	wat := compileToWAT(t, `function f(a: boolean, b: boolean): boolean { return a && b; }`)
	// `&&` lowers to a conditional that yields i32 on both arms.
	mustContain(t, wat, "if (result i32)")
}

func TestIfElse(t *testing.T) {
	wat := compileToWAT(t, `function f(n: number): number {
		if (n == 0) { return 1; } else { return 2; }
	}`)
	mustContain(t, wat, "if")
	mustContain(t, wat, "else")
	mustContain(t, wat, "end")
}

func TestWhileBreakContinue(t *testing.T) {
	wat := compileToWAT(t, `function f(): number {
		var i = 0;
		while (true) {
			if (i == 5) { break; }
			i = i + 1;
		}
		return i;
	}`)
	mustContain(t, wat, "block $break")
	mustContain(t, wat, "loop $loop")
	mustContain(t, wat, "br $break")
}

func TestForLoopWithStep(t *testing.T) {
	wat := compileToWAT(t, `function f(): number {
		var sum = 0;
		for (var i = 0; i < 10; i = i + 1) {
			if (i < 5) { continue; }
			sum = sum + i;
		}
		return sum;
	}`)
	// continue must `br` out of the inner block (the $cont label) so
	// the step still runs.
	mustContain(t, wat, "block $cont")
	mustContain(t, wat, "br $cont")
}

func TestRecursionDirectCall(t *testing.T) {
	wat := compileToWAT(t, `function fact(n: number): number {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}`)
	mustContain(t, wat, "call $fact")
}

func TestStringsLowerToLinearMemory(t *testing.T) {
	wat := compileToWAT(t, `function main(): void { print("hi"); }`)
	mustContain(t, wat, `(import "wasi_snapshot_preview1" "fd_write"`)
	mustContain(t, wat, "(memory $mem 1)")
	mustContain(t, wat, `(func $print`)
	mustContain(t, wat, `(func $putchar`)
	mustContain(t, wat, "call $print")
	// Length-prefixed string entry: 2 bytes "hi" so prefix is \02\00\00\00.
	mustContain(t, wat, `\02\00\00\00hi`)
	// Pointer to the chars (after the 4-byte prefix) starts at 64+4=68.
	mustContain(t, wat, "i32.const 68")
}

// Programs that don't touch strings, print or putchar should stay free
// of the WASI import and runtime helpers.
func TestNoRuntimeWhenUnused(t *testing.T) {
	wat := compileToWAT(t, `function main(): number { return 42; }`)
	if strings.Contains(wat, "wasi_snapshot_preview1") {
		t.Errorf("WASI import emitted unnecessarily:\n%s", wat)
	}
	if strings.Contains(wat, "(memory $mem") {
		t.Errorf("memory emitted unnecessarily:\n%s", wat)
	}
	if strings.Contains(wat, "$__lang_alloc") {
		t.Errorf("bump allocator emitted unnecessarily:\n%s", wat)
	}
	if strings.Contains(wat, "(table") {
		t.Errorf("function table emitted unnecessarily:\n%s", wat)
	}
}

// Array literals lower to bump-allocator + per-element store, with a
// fresh i32 local holding the base pointer.
func TestArrayLitEmitsAllocAndStores(t *testing.T) {
	wat := compileToWAT(t, `function f(): number {
		var a: number[] = [10, 20, 30];
		return a[1];
	}`)
	mustContain(t, wat, "(func $__lang_alloc")
	mustContain(t, wat, "(local $__arr_0 i32)")
	// 4 (length prefix) + 3*4 (elements) = 16
	mustContain(t, wat, "i32.const 16")
	mustContain(t, wat, "call $__lang_alloc")
	// Indexing `a[1]` goes through the bounds-check helper.
	mustContain(t, wat, "call $__arr_idx")
	mustContain(t, wat, "i32.load")
}

// Index assignment lowers to i32.store and leaves nothing on the
// stack; no `drop` should be emitted by the surrounding ExprStmt.
func TestIndexAssignEmitsStore(t *testing.T) {
	wat := compileToWAT(t, `function f(): number {
		var a: number[] = [0, 0];
		a[0] = 7;
		return a[0];
	}`)
	mustContain(t, wat, "i32.store")
}

// Function-typed parameters trigger a function table, type
// declarations for the indirect callee's signature, and a
// call_indirect at the call site. Only functions that are actually
// referenced as values (`add`) end up in the table; non-value
// callers (`apply`, `main`) stay out.
func TestIndirectCallEmitsTable(t *testing.T) {
	wat := compileToWAT(t, `
		function add(a: number, b: number): number { return a + b; }
		function apply(f: (number, number) => number, a: number, b: number): number {
			return f(a, b);
		}
		function main(): number { return apply(add, 40, 2); }`)
	mustContain(t, wat, "(type $t0 (func (param i32) (param i32) (result i32)))")
	mustContain(t, wat, "(table $fns 1 funcref)")
	mustContain(t, wat, "(elem (i32.const 0) $add)")
	mustContain(t, wat, "call_indirect (type $t0)")
	// `apply(add, ...)` pushes add's table index (= 0).
	mustContain(t, wat, "i32.const 0")
}

// `var f = name` in source becomes an i32.const for the table index;
// calling f goes through call_indirect.
func TestFunctionValueLocal(t *testing.T) {
	wat := compileToWAT(t, `
		function add(a: number, b: number): number { return a + b; }
		function main(): number {
			var f = add;
			return f(40, 2);
		}`)
	// Only `add` is in the table — main isn't referenced as a value.
	mustContain(t, wat, "(table $fns 1 funcref)")
	mustContain(t, wat, "call_indirect (type $t0)")
}

func TestFloatLiteralAndArithmetic(t *testing.T) {
	wat := compileToWAT(t, `function main(): float { return 1.5 + 2.5; }`)
	mustContain(t, wat, "(result f32)")
	mustContain(t, wat, "f32.const 1.5")
	mustContain(t, wat, "f32.const 2.5")
	mustContain(t, wat, "f32.add")
}

func TestFloatNegate(t *testing.T) {
	wat := compileToWAT(t, `function f(x: float): float { return -x; }`)
	mustContain(t, wat, "f32.neg")
}

func TestFloatComparison(t *testing.T) {
	wat := compileToWAT(t, `function f(x: float, y: float): boolean { return x < y; }`)
	mustContain(t, wat, "f32.lt")
}

func TestFloatLocalAndParam(t *testing.T) {
	wat := compileToWAT(t, `function f(x: float): float { var y: float = x; return y; }`)
	mustContain(t, wat, "(param $x f32)")
	mustContain(t, wat, "(local $y f32)")
}

func TestSwitchEmitsBlockAndScratch(t *testing.T) {
	wat := compileToWAT(t, `function f(n: number): number {
		switch (n) {
			case 1: return 10;
			case 2, 3: return 20;
			default: return 99;
		}
		return -1;
	}`)
	mustContain(t, wat, "(local $__sw_1 i32)")
	mustContain(t, wat, "block $sw_end")
	mustContain(t, wat, "i32.eq")
	mustContain(t, wat, "i32.or")
}

func TestTernaryEmitsIfResult(t *testing.T) {
	wat := compileToWAT(t, `function f(b: boolean): number { return b ? 1 : 2; }`)
	mustContain(t, wat, "if (result i32)")
	mustContain(t, wat, "i32.const 1")
	mustContain(t, wat, "i32.const 2")
}

func TestTernaryFloatEmitsF32Result(t *testing.T) {
	wat := compileToWAT(t, `function f(b: boolean): float { return b ? 1.5 : 2.5; }`)
	mustContain(t, wat, "if (result f32)")
}

func TestCompoundAssignLowersToBinary(t *testing.T) {
	wat := compileToWAT(t, `function f(): number { var x: number = 5; x += 7; return x; }`)
	// `x += 7` lowers to `x = x + 7`, so we expect a `local.get`,
	// `i32.const 7`, `i32.add`, `local.tee`.
	mustContain(t, wat, "i32.const 7")
	mustContain(t, wat, "i32.add")
	mustContain(t, wat, "local.tee $x")
}

func TestStringIndexEmitsLoad8(t *testing.T) {
	wat := compileToWAT(t, `function f(): number { var s: string = "abc"; return s[1]; }`)
	mustContain(t, wat, "i32.load8_u")
}

func TestStringEqualityEmitsHelper(t *testing.T) {
	wat := compileToWAT(t, `function f(): boolean { return "a" == "a"; }`)
	mustContain(t, wat, "$__str_eq")
	mustContain(t, wat, "call $__str_eq")
}

func TestLenOfStringInlinesPrefixLoad(t *testing.T) {
	wat := compileToWAT(t, `function f(): number { return len("hello"); }`)
	// len is an inline `i32.load (s - 4)`, not a call.
	mustContain(t, wat, "i32.const 4")
	mustContain(t, wat, "i32.sub")
	mustContain(t, wat, "i32.load")
	if strings.Contains(wat, "call $len") {
		t.Errorf("expected len to be inlined, got call $len in:\n%s", wat)
	}
}

func TestStructLitAllocatesAndStores(t *testing.T) {
	wat := compileToWAT(t, `struct P { x: number, y: number }
		function main(): number {
			var p: P = P { x: 1, y: 2 };
			return p.x + p.y;
		}`)
	mustContain(t, wat, "(func $__lang_alloc")
	mustContain(t, wat, "(local $__sl_0 i32)")
	mustContain(t, wat, "i32.const 8") // 2 fields × 4 bytes
	mustContain(t, wat, "i32.store")
	mustContain(t, wat, "i32.load")
}

func TestStringConcatEmitsHelper(t *testing.T) {
	wat := compileToWAT(t, `function main(): void { print("a" + "b"); }`)
	mustContain(t, wat, "$__str_concat")
	mustContain(t, wat, "call $__str_concat")
}

func TestArrayIndexBoundsChecked(t *testing.T) {
	wat := compileToWAT(t, `function f(): number {
		var a: number[] = [1, 2, 3];
		return a[0];
	}`)
	mustContain(t, wat, "$__arr_idx")
	mustContain(t, wat, "call $__arr_idx")
	mustContain(t, wat, "unreachable")
}

func TestStringIndexBoundsChecked(t *testing.T) {
	wat := compileToWAT(t, `function f(): number { var s: string = "abc"; return s[0]; }`)
	mustContain(t, wat, "$__str_idx")
	mustContain(t, wat, "call $__str_idx")
}

func TestClosureHoistsAndCaptures(t *testing.T) {
	wat := compileToWAT(t, `function makeAdder(n: number): (number) => number {
		function add(x: number): number { return x + n; }
		return add;
	}
	function main(): number {
		var f = makeAdder(7);
		return f(35);
	}`)
	// The inner function gets hoisted with a synthetic name and an
	// __env parameter; closure cells live at offset 64.
	mustContain(t, wat, "$__closure_add_")
	mustContain(t, wat, "(param $__env i32)")
	// MakeClosure allocates 8 bytes for the closure pair and stores
	// the env pointer at +4.
	mustContain(t, wat, "$__cl_scratch")
	mustContain(t, wat, "$__env_scratch")
	// Indirect call through the closure dispatches via call_indirect.
	mustContain(t, wat, "call_indirect")
}
