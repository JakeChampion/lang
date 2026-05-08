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

// In legacy mode (no closures), function values are bare table
// indices. The funcref table is built from the inTable subset of
// prog.Funcs in declaration order, so the table position of a
// value-referenced function depends on how many earlier functions
// are themselves in the table — NOT on its funcIndex (raw position
// in prog.Funcs). This program declares two non-table functions
// before the value-referenced `target`, so funcIndex["target"] = 2
// but tableIndex["target"] = 0. The pushed value must be 0.
func TestFunctionValueUsesTableIndexNotFuncIndex(t *testing.T) {
	src := `function unrelated_a(x: number): number { return x + 1; }
	function unrelated_b(x: number): number { return x + 2; }
	function target(x: number): number { return x * 10; }
	function apply(f: (number) => number, x: number): number {
		return f(x);
	}
	function main(): number { return apply(target, 4); }`
	wat := compileToWAT(t, src)
	// The funcref table holds only `target` (index 0). The value
	// pushed for `target` in `apply(target, 4)` must be 0, matching
	// the table position, not 2 (its funcIndex).
	mustContain(t, wat, "(table $fns 1 funcref)")
	mustContain(t, wat, "(elem (i32.const 0) $target)")
	// Inside main, the call is `i32.const 0; i32.const 4; call $apply`.
	// Spot-check that we don't see `i32.const 2` (funcIndex of target)
	// followed immediately by the apply call.
	if strings.Contains(wat, "i32.const 2\n        i32.const 4\n        call $apply") {
		t.Errorf("emitted funcIndex (2) where tableIndex (0) was needed:\n%s", wat)
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
	// Use parameters to keep ops visible past constant folding
	// (literal arithmetic would collapse to a single i32.const).
	// `%` is intentionally still here — Fold deliberately leaves
	// rem_s alone so a `1 % 0` source bug surfaces at runtime
	// rather than getting silently masked.
	wat := compileToWAT(t, `function main(a: number, b: number): number { return 7 % 3 + (a & b); }`)
	mustContain(t, wat, "i32.rem_s")
	mustContain(t, wat, "i32.and")
	mustContain(t, wat, "i32.add")
}

func TestComparisonReturnsI32(t *testing.T) {
	wat := compileToWAT(t, `function f(a: number, b: number): boolean { return a < b; }`)
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
	// while expands to outer `block` + `loop`, with `break` resolving
	// to a `br` that targets the outer block at relative depth 2 from
	// inside the if-body.
	mustContain(t, wat, "block")
	mustContain(t, wat, "loop")
	mustContain(t, wat, "br ")
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
	// `for` adds an inner `block` around the body so `continue` can
	// jump just past it (and so the step still runs before the next
	// iteration). br_if exits the outer block when the condition
	// fails.
	mustContain(t, wat, "block")
	mustContain(t, wat, "loop")
	mustContain(t, wat, "br_if")
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
	// After step 6 every print goes through wasi:io/streams; the
	// preview-1 fd_write import is gone.
	mustContain(t, wat, `(import "wasi:io/streams@0.2.0" "[method]output-stream.blocking-write-and-flush"`)
	mustContain(t, wat, `(import "wasi:cli/stdout@0.2.0" "get-stdout"`)
	mustContain(t, wat, "(memory $mem 1)")
	mustContain(t, wat, `(func $print`)
	mustContain(t, wat, `(func $putchar`)
	mustContain(t, wat, "call $print")
	// Length-prefixed string entry: 2 bytes "hi" so prefix is \02\00\00\00.
	mustContain(t, wat, `\02\00\00\00hi`)
	// Preview-2 layout pushes the string base from 64 to 128 (the
	// canonical-ABI scratch slots reserve memory[92..127]); chars
	// for the first interned string therefore start at 128+4=132.
	mustContain(t, wat, "i32.const 132")
}

// Programs that don't touch strings, print or putchar still pull in
// the runtime preamble after step 6 — the component model exports
// `cabi_realloc` and the host always wires
// `wasi:io/streams.blocking-write-and-flush` whether the program
// uses it or not. The test now asserts the *minimum* shape under
// preview-2: stdio imports come in unconditionally, the bump
// allocator is required (cabi_realloc defers to it), and the
// preview-1 imports are gone.
func TestNoRuntimeWhenUnused(t *testing.T) {
	wat := compileToWAT(t, `function main(): number { return 42; }`)
	if strings.Contains(wat, "wasi_snapshot_preview1") {
		t.Errorf("preview-1 import leaked into WAT:\n%s", wat)
	}
	mustContain(t, wat, `(import "wasi:cli/stdout@0.2.0" "get-stdout"`)
	mustContain(t, wat, `$__lang_alloc`)
	mustContain(t, wat, `(export "cabi_realloc" (func $cabi_realloc))`)
}

// Array literals lower to bump-allocator + per-element store, with a
// synthetic i32 scratch local holding the base pointer between the
// alloc and the stores.
func TestArrayLitEmitsAllocAndStores(t *testing.T) {
	wat := compileToWAT(t, `function f(): number {
		var a: number[] = [10, 20, 30];
		return a[1];
	}`)
	mustContain(t, wat, "(func $__lang_alloc")
	mustContain(t, wat, "(local $__scratch_0 i32)")
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
	// The tag is stashed in a synthetic i32 scratch slot. Each case
	// expands to a pair of nested blocks (skip-on-no-match outer +
	// match-target inner) using br_if for value comparisons.
	mustContain(t, wat, "(local $__scratch_0 i32)")
	mustContain(t, wat, "block")
	mustContain(t, wat, "i32.eq")
	mustContain(t, wat, "br_if")
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
	// `x += 7` lowers to `x = x + 7`. The IR encodes the tee with a
	// (store, load) pair the FuseTee pass collapses into a single
	// OpTeeLocal — codegen emits `local.tee $x`.
	mustContain(t, wat, "i32.const 7")
	mustContain(t, wat, "i32.add")
	mustContain(t, wat, "local.tee $x")
}

func TestStringIndexEmitsLoad8(t *testing.T) {
	wat := compileToWAT(t, `function f(): number { var s: string = "abc"; return s[1]; }`)
	mustContain(t, wat, "i32.load8_u")
}

// Use a parameter to defeat the IR-level lit==lit fold; we
// want to see the runtime $__str_eq path.
func TestStringEqualityEmitsHelper(t *testing.T) {
	wat := compileToWAT(t, `function f(s: string): boolean { return s == "ok"; }`)
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
	mustContain(t, wat, "(local $__scratch_0 i32)")
	mustContain(t, wat, "i32.const 8") // 2 fields × 4 bytes
	mustContain(t, wat, "i32.store")
	mustContain(t, wat, "i32.load")
}

func TestStringConcatEmitsHelper(t *testing.T) {
	// Use a parameter to defeat the IR-level `literal+literal`
	// fold (which would otherwise collapse `"a" + "b"` to a
	// single OpConstStr "ab" in the IR builder).
	wat := compileToWAT(t, `function f(s: string): string { return s + "b"; }`)
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
