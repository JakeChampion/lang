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
