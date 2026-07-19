package x86_64

// Thin, hermetic property tests for the x86-64 backend.
//
// Until now the codegen packages had no in-package tests — every
// regression surfaced only as a full end-to-end failure (compile,
// link, run under qemu), which makes "what broke" expensive to
// localize. These tests exercise Emit directly on small Fern
// programs and assert *properties of the emitted assembly text*,
// with no external toolchain (no assembler, linker, or qemu) and no
// execution. A break here points straight at the emitter.
//
// The assertions are deliberately spelling-independent: they don't
// pin specific mnemonics or operand syntax (which churn), only
// invariants that must hold for any correct emitter — determinism,
// error-free lowering across the language surface, presence of the
// program entry symbol, and that more code emits more assembly.

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// compile runs the front of the pipeline the CLI driver uses
// (parse → check → monomorph) and returns the emitted x86-64
// assembly text. It fails the test on any stage error.
func compile(t *testing.T, src string) string {
	t.Helper()
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
	asm, err := Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return asm
}

// featureMatrix is a spread of small programs, each self-contained
// (defines main, no imports), covering the core language surface the
// backend must lower: arithmetic, control flow, recursion (TCO),
// loops, structs + receiver methods, function values, and string
// output. Each must emit without error and produce a non-empty body
// with the program entry symbol.
var featureMatrix = map[string]string{
	"minimal": `
function main(): i32 { return 0; }`,

	"arithmetic": `
function main(): i32 {
	var x: i32 = 6;
	var y: i32 = 7;
	return x * y + (y - x) / 2;
}`,

	"if_else": `
function classify(n: i32): i32 {
	if (n > 0) { return 1; } else if (n < 0) { return -1; }
	return 0;
}
function main(): i32 { return classify(-5) + classify(5) + classify(0); }`,

	"while_loop": `
function main(): i32 {
	var i: i32 = 0;
	var sum: i32 = 0;
	while (i < 10) { sum = sum + i; i = i + 1; }
	return sum;
}`,

	"recursion_tco": `
function fact(n: i32, acc: i32): i32 {
	if (n == 0) { return acc; }
	return fact(n - 1, acc * n);
}
function main(): i32 { return fact(5, 1); }`,

	"struct_method": `
struct Point { x: i32, y: i32 }
function (p: Point) sq(): i32 { return p.x * p.x + p.y * p.y; }
function main(): i32 {
	var p: Point = Point { x: 3, y: 4 };
	return p.sq();
}`,

	"function_value": `
function add(a: i32, b: i32): i32 { return a + b; }
function apply(f: (i32, i32) => i32, a: i32, b: i32): i32 { return f(a, b); }
function main(): i32 { return apply(add, 20, 22); }`,

	"string_print": `
function main(): i32 {
	print("hello");
	return 0;
}`,

	"bitwise": `
function main(): i32 {
	var a: i32 = 0xF0;
	var b: i32 = 0x0F;
	return (a | b) & 0xFF ^ (a >> 2);
}`,

	// string_concat — exercises __fern_strcat / __fern_str_concat
	// plus literal interning of the operands.
	"string_concat": `
function main(): i32 {
	var s: string = "hello" + " " + "world";
	return s.len();
}`,

	// array_iterate — exercises array literal allocation, .push, and
	// `for x in arr` lowering with element-load + body codegen.
	"array_iterate": `
function main(): i32 {
	var xs: i32[] = [1, 2, 3];
	xs = xs.append(4);
	var sum: i32 = 0;
	for x in xs { sum = sum + x; }
	return sum;
}`,

	// match_option — exercises Option box construction, match
	// dispatch, and payload binding through the variant shape.
	"match_option": `
function maybe(n: i32): Option[i32] {
	if (n > 0) { return Some(n); }
	return None;
}
function main(): i32 {
	match (maybe(7)) {
		Some(v) => { return v; },
		None => { return 0; }
	}
	return -1;
}`,

	// closure_capture — exercises the capturing-closure path: env
	// block alloc, capture layout, closure call convention (%r10
	// for the env ptr), and OpMakeClosure / OpMakeEnv lowering.
	// The bare-function `function_value` case above hits the
	// no-capture path; this one rides the harder emit surface
	// that historically has been a bug source.
	"closure_capture": `
function adder(n: i32): (i32) => i32 {
	return function (x: i32): i32 { return x + n; };
}
function main(): i32 {
	var add10: (i32) => i32 = adder(10);
	var add20: (i32) => i32 = adder(20);
	return add10(1) + add20(2);
}`,

	// closure_string_capture — extends closure_capture with a
	// STRING capture. Exercises the string-closure-capture path
	// (Slice 6, the generated __closure_drop_<name> thunk runs
	// __fern_str_dec on the captured slot). A regression here
	// (wrong width on the load, or missed string-type
	// classification in hasRcCapture) would silently leak or
	// double-free the captured string.
	"closure_string_capture": `
function greeter(name: string): () => string {
	return function (): string { return "hello, " + name; };
}
function main(): i32 {
	var g: () => string = greeter("world");
	return g().len();
}`,
}

// TestEmitFeatureMatrix asserts every program in the matrix lowers
// and emits a non-empty body containing the entry symbol. This is
// the per-feature regression net: a backend gap shows up as a named
// subtest failure rather than an opaque e2e break.
func TestEmitFeatureMatrix(t *testing.T) {
	for name, src := range featureMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			asm := compile(t, src)
			if strings.TrimSpace(asm) == "" {
				t.Fatal("emitted assembly is empty")
			}
			if !strings.Contains(asm, "_start") {
				t.Errorf("emitted assembly missing program entry symbol `_start`:\n%s", asm)
			}
		})
	}
}

// TestEmitDeterministic guards against nondeterministic codegen
// (e.g. map-iteration-order leaking into output) — a real hazard
// that breaks reproducible builds and byte-identical self-host
// gates. Emitting the same source repeatedly must be byte-identical.
func TestEmitDeterministic(t *testing.T) {
	for name, src := range featureMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			first := compile(t, src)
			for i := 0; i < 4; i++ {
				again := compile(t, src)
				if again != first {
					t.Fatalf("emit not deterministic on run %d: output differs", i+2)
				}
			}
		})
	}
}

// TestEmitGrowsWithCode is a coarse "the emitter actually walked the
// body" check: a program with substantially more reachable code must
// produce more assembly than a trivial one. Catches a whole class of
// regressions where a function is silently dropped or a body fails
// to lower yet still returns without error.
func TestEmitGrowsWithCode(t *testing.T) {
	small := compile(t, featureMatrix["minimal"])
	big := compile(t, featureMatrix["recursion_tco"])
	if len(big) <= len(small) {
		t.Fatalf("expected non-trivial program to emit more assembly than minimal: big=%d bytes, small=%d bytes", len(big), len(small))
	}
}

// TestRcHelpersGuardBelowHeap pins the below-heap guard in the refcount
// helpers (the x86-64 mirror of the arm64 test). The exit-dec sweep
// decrements every local slot at function exit, including ones holding
// non-heap values (a non-pointer scalar or a no-capture closure's bare
// code pointer). Only heap-allocated objects carry an rc word at [ptr-8],
// so the helpers must skip any pointer below the heap base (0x10000000,
// the mmap hint) — otherwise the helper writes [ptr-8] of a .text/.rodata
// address. The old guard only rejected < 0x10000.
func TestRcHelpersGuardBelowHeap(t *testing.T) {
	src := `struct Box { v: i32 }
function main(): i32 {
	var a: Box = Box { v: 1 };
	var b: Box = a;
	return b.v;
}`
	asm := compile(t, src)
	if !strings.Contains(asm, "__fern_rc_dec:") {
		t.Fatal("__fern_rc_dec helper not emitted by a program that drops a heap value")
	}
	for _, helper := range []string{"__fern_rc_dec", "__fern_rc_inc"} {
		i := strings.Index(asm, helper+":")
		if i < 0 {
			continue
		}
		body := asm[i:]
		if j := strings.Index(body[len(helper)+1:], "\n.globl "); j >= 0 {
			body = body[:len(helper)+1+j]
		}
		if !strings.Contains(body, "0x10000000") {
			t.Errorf("%s is missing the below-heap guard (no `cmp rdi, 0x10000000`):\n%s", helper, body)
		}
	}
}

// A negative i32 constant must materialise into the full 64-bit register
// SIGN-extended, not zero-extended: i32 operand-stack values feed 64-bit
// arithmetic (index / slice / pointer math), so `mov eax, -2` (which
// zero-extends to rax = 0x00000000FFFFFFFE) corrupts a later 64-bit use
// and trips the bounds-check trap. The fix emits `mov rax, N` (REX.W C7
// /0, sign-extends). A non-negative constant keeps the compact `mov eax`.
// Regression for the modloader.parent_dir `dir.len() - 1 - 1` crash.
func TestNegativeConstSignExtends(t *testing.T) {
	// `x + (-2)` keeps the -2 as a bare negative i32 const in the IR.
	asm := compile(t, `function f(x: i32): i32 { return x + (0 - 2); }
function main(): i32 { return f(5); }`)
	if !strings.Contains(asm, "mov rax, -2") {
		t.Errorf("negative i32 const should emit sign-extending `mov rax, -2`:\n%s", asm)
	}
	if strings.Contains(asm, "mov eax, -2") {
		t.Errorf("negative i32 const must NOT emit zero-extending `mov eax, -2`:\n%s", asm)
	}
	// A non-negative constant still uses the compact 32-bit form.
	pos := compile(t, `function main(): i32 { return 7; }`)
	if !strings.Contains(pos, "mov eax, 7") {
		t.Errorf("non-negative i32 const should keep `mov eax, 7`:\n%s", pos)
	}
}
