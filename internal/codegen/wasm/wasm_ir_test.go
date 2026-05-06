package wasm

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/closureconv"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

// emitBoth parses + checks src, then emits via both Emit (AST walker)
// and EmitFromIR (IR walker). Returns both WAT strings so callers can
// compare validity / shape / behaviour.
//
// Both emitters mutate the AST via closure conversion; we run Lower
// first (which calls Convert) and then call Emit on the same prog —
// Emit's Convert call is a no-op the second time around because the
// IsLocal nodes have already been hoisted.
func emitBoth(t *testing.T, src string) (astWAT, irWAT string) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// Lower runs closureconv internally. The mutated prog is what
	// Emit will see, so its Convert call finds no IsLocal nodes left
	// and is a no-op.
	ip, err := ir.Lower(prog, info)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	irWAT, err = EmitFromIR(prog, info, ip)
	if err != nil {
		t.Fatalf("EmitFromIR: %v", err)
	}
	astWAT, err = Emit(prog, info)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	return astWAT, irWAT
}

// emitIRDirect runs only the IR pipeline. Used when the test only
// cares about the IR-side output and we don't want Emit's second
// closureconv pass to re-run.
func emitIRDirect(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := closureconv.Convert(prog, info); err != nil {
		t.Fatalf("convert: %v", err)
	}
	ip, err := ir.Lower(prog, info)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	wat, err := EmitFromIR(prog, info, ip)
	if err != nil {
		t.Fatalf("EmitFromIR: %v", err)
	}
	return wat
}

// shouldContain asserts each of the given substrings appears in wat.
// We don't pin byte-for-byte equivalence with the AST emitter — the
// two paths can format the same module differently — but checking
// for the structural pieces (`(module`, `(func $...)`, the right
// instructions) gives a quick sanity gate.
func shouldContain(t *testing.T, wat string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(wat, w) {
			t.Errorf("expected %q in WAT:\n%s", w, wat)
		}
	}
}

func TestIRWASMEmitsModule(t *testing.T) {
	wat := emitIRDirect(t, `function main(): number { return 42; }`)
	shouldContain(t, wat,
		"(module",
		"(func $main",
		"(result i32)",
		"i32.const 42",
		"return",
	)
}

func TestIRWASMArithmetic(t *testing.T) {
	wat := emitIRDirect(t, `function main(): number { return 1 + 2 * 3; }`)
	shouldContain(t, wat,
		"i32.const 1",
		"i32.const 2",
		"i32.const 3",
		"i32.mul",
		"i32.add",
	)
}

func TestIRWASMIfElse(t *testing.T) {
	src := `function main(): number {
		if (1 == 1) { return 10; } else { return 20; }
	}`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat,
		"i32.eq",
		"if",
		"else",
		"end",
	)
}

func TestIRWASMWhileWithBreak(t *testing.T) {
	src := `function main(): number {
		var i: number = 0;
		while (i < 10) {
			if (i == 5) { break; }
			i = i + 1;
		}
		return i;
	}`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat,
		"block",
		"loop",
		"br_if",
		"br ",
	)
}

func TestIRWASMForLoop(t *testing.T) {
	src := `function main(): number {
		var sum: number = 0;
		for (var i: number = 0; i < 5; i = i + 1) {
			sum = sum + i;
		}
		return sum;
	}`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat, "block", "loop", "br_if")
}

func TestIRWASMSwitch(t *testing.T) {
	src := `function f(n: number): number {
		switch (n) {
			case 1, 2: return 10;
			case 3: return 30;
			default: return 0;
		}
		return -1;
	}`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat, "block", "br_if", "i32.eq")
}

func TestIRWASMTernary(t *testing.T) {
	wat := emitIRDirect(t, `function main(b: boolean): number { return b ? 1 : 2; }`)
	shouldContain(t, wat, "if (result i32)", "else", "end")
}

func TestIRWASMShortCircuitAnd(t *testing.T) {
	wat := emitIRDirect(t, `function f(a: boolean, b: boolean): boolean { return a && b; }`)
	shouldContain(t, wat, "if (result i32)", "i32.const 0")
}

func TestIRWASMDirectCall(t *testing.T) {
	src := `function add(a: number, b: number): number { return a + b; }
	function main(): number { return add(2, 3); }`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat, "(func $add", "(func $main", "call $add")
}

func TestIRWASMRecursion(t *testing.T) {
	src := `function fact(n: number): number {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}
	function main(): number { return fact(5); }`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat, "call $fact", "i32.mul")
}

func TestIRWASMStringConstAndPrint(t *testing.T) {
	src := `function main(): void { print("hi"); }`
	wat := emitIRDirect(t, src)
	// The string lives in linear memory — pool address is stable so
	// asserting on `i32.const ` + `call $print` is enough.
	shouldContain(t, wat, "call $print", "(memory $mem")
}

func TestIRWASMArrayAndIndex(t *testing.T) {
	src := `function main(): number {
		var a: number[] = [10, 20, 30];
		return a[1];
	}`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat, "call $__lang_alloc", "call $__arr_idx", "i32.load")
}

func TestIRWASMStringIndex(t *testing.T) {
	src := `function main(): number {
		var s: string = "abc";
		return s[1];
	}`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat, "call $__str_idx", "i32.load8_u")
}

func TestIRWASMStruct(t *testing.T) {
	src := `struct P { x: number, y: number }
	function main(): number {
		var p: P = P { x: 10, y: 32 };
		return p.x + p.y;
	}`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat, "call $__lang_alloc", "i32.load", "i32.add")
}

func TestIRWASMStringConcat(t *testing.T) {
	src := `function main(): number {
		var s: string = "a" + "b";
		return len(s);
	}`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat, "call $__str_concat", "i32.const 4", "i32.sub", "i32.load")
}

func TestIRWASMStringEquality(t *testing.T) {
	src := `function main(): number {
		if ("a" == "b") { return 1; }
		return 0;
	}`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat, "call $__str_eq")
}

func TestIRWASMFloat(t *testing.T) {
	src := `function main(): float { return 1.5 + 2.5; }`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat, "f32.const", "f32.add")
}

func TestIRWASMIndirectCall(t *testing.T) {
	src := `function add(a: number, b: number): number { return a + b; }
	function apply(f: (number, number) => number, a: number, b: number): number {
		return f(a, b);
	}
	function main(): number { return apply(add, 2, 3); }`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat, "(table $fns", "call_indirect", "(type $t0")
}

func TestIRWASMNestedClosure(t *testing.T) {
	src := `function outer(): number {
		var n: number = 5;
		function inner(): number { return n + 1; }
		return inner();
	}
	function main(): number { return outer(); }`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat,
		"(func $__closure_inner_",
		"call $__lang_alloc",
		"i32.store",
		"call_indirect",
	)
}

func TestIRWASMArrayElementAssignment(t *testing.T) {
	src := `function main(): number {
		var a: number[] = [10, 20, 30];
		a[1] = 99;
		return a[1];
	}`
	wat := emitIRDirect(t, src)
	// Index assignment: bounds-checked address compute + store.
	shouldContain(t, wat, "call $__arr_idx", "i32.store")
}

func TestIRWASMFieldAssignment(t *testing.T) {
	src := `struct P { x: number, y: number }
	function main(): number {
		var p: P = P { x: 1, y: 2 };
		p.y = 99;
		return p.y;
	}`
	wat := emitIRDirect(t, src)
	shouldContain(t, wat, "i32.store", "i32.load")
}

func TestIRWASMBothEmittersValid(t *testing.T) {
	// A representative program that exercises most language features.
	// Both emitters must produce *some* WAT (we don't compare byte-
	// for-byte). This catches gross regressions where the IR walker
	// returns an error or skips an op.
	src := `function fact(n: number): number {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}
	function main(): number {
		var a: number[] = [1, 2, 3];
		var sum: number = 0;
		for (var i: number = 0; i < 3; i = i + 1) {
			sum = sum + a[i];
		}
		return sum + fact(4);
	}`
	astWAT, irWAT := emitBoth(t, src)
	if !strings.Contains(astWAT, "(module") {
		t.Errorf("AST emitter produced no module")
	}
	if !strings.Contains(irWAT, "(module") {
		t.Errorf("IR emitter produced no module")
	}
}
