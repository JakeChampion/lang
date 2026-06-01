package arm64

// Thin, hermetic property tests for the arm64 backend — the
// companion to internal/codegen/x86_64/emit_property_test.go.
//
// arm64.go is the largest codegen file in the tree (~7.8k lines) and,
// like the other backends, had no in-package tests: emitter
// regressions only surfaced as full end-to-end failures (compile,
// cross-link, run under qemu), which makes "what broke" expensive to
// localize. These tests call Emit directly on small Fern programs and
// assert *properties of the emitted assembly text*, with no external
// toolchain (no assembler, linker, or qemu) and no execution. A break
// here points straight at the emitter.
//
// As with the x86-64 suite the assertions are spelling-independent:
// they pin invariants any correct emitter must satisfy — error-free
// lowering across the language surface, determinism, presence of the
// program entry symbol, and that more code emits more assembly — not
// specific mnemonics or operand syntax (which churn). The Linux and
// Darwin (Mach-O) emit paths are both exercised, since the entry
// symbol and syscall conventions differ between them.

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// compile runs the front of the pipeline the CLI driver uses
// (parse → check → monomorph) and returns the emitted arm64 assembly
// text for the given options. It fails the test on any stage error.
func compile(t *testing.T, src string, opts Options) string {
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
	asm, err := EmitWithOptions(prog, info, opts)
	if err != nil {
		t.Fatalf("emit (Darwin=%v): %v", opts.Darwin, err)
	}
	return asm
}

// hasEntrySymbol reports whether asm defines a program entry point.
// Linux ELF emits `_start`; Darwin/Mach-O emits `_main` (linked
// against by dyld via LC_MAIN). Accept either so the same assertion
// works on both emit paths.
func hasEntrySymbol(asm string) bool {
	return strings.Contains(asm, "_start") || strings.Contains(asm, "_main")
}

// featureMatrix is a spread of small self-contained programs (each
// defines main, no imports) covering the core language surface the
// backend must lower: arithmetic, control flow, recursion (TCO),
// loops, structs + receiver methods, function values, string output,
// and bitwise ops. Mirrors the x86-64 suite so the two backends stay
// covered in lockstep.
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

// optionVariants is the set of emit configurations every feature must
// lower under: Linux ELF (default) and Darwin/Mach-O.
var optionVariants = map[string]Options{
	"linux":  {},
	"darwin": {Darwin: true},
}

// TestEmitFeatureMatrix asserts every program lowers and emits a
// non-empty body containing a program entry symbol, on both the Linux
// and Darwin emit paths. A backend gap shows up as a named subtest
// failure rather than an opaque e2e break.
func TestEmitFeatureMatrix(t *testing.T) {
	for variant, opts := range optionVariants {
		variant, opts := variant, opts
		t.Run(variant, func(t *testing.T) {
			for name, src := range featureMatrix {
				name, src := name, src
				t.Run(name, func(t *testing.T) {
					asm := compile(t, src, opts)
					if strings.TrimSpace(asm) == "" {
						t.Fatal("emitted assembly is empty")
					}
					if !hasEntrySymbol(asm) {
						t.Errorf("emitted assembly missing program entry symbol (`_start`/`_main`):\n%s", asm)
					}
				})
			}
		})
	}
}

// TestEmitDeterministic guards against nondeterministic codegen (e.g.
// map-iteration-order leaking into output) — a real hazard that
// breaks reproducible builds and the byte-identical self-host gates.
// Emitting the same source repeatedly must be byte-identical.
func TestEmitDeterministic(t *testing.T) {
	for variant, opts := range optionVariants {
		variant, opts := variant, opts
		t.Run(variant, func(t *testing.T) {
			for name, src := range featureMatrix {
				name, src := name, src
				t.Run(name, func(t *testing.T) {
					first := compile(t, src, opts)
					for i := 0; i < 4; i++ {
						again := compile(t, src, opts)
						if again != first {
							t.Fatalf("emit not deterministic on run %d: output differs", i+2)
						}
					}
				})
			}
		})
	}
}

// TestEmitGrowsWithCode is a coarse "the emitter actually walked the
// body" check: a program with substantially more reachable code must
// produce more assembly than a trivial one. Catches a class of
// regressions where a function is silently dropped or a body fails to
// lower yet still returns without error.
func TestEmitGrowsWithCode(t *testing.T) {
	small := compile(t, featureMatrix["minimal"], Options{})
	big := compile(t, featureMatrix["recursion_tco"], Options{})
	if len(big) <= len(small) {
		t.Fatalf("expected non-trivial program to emit more assembly than minimal: big=%d bytes, small=%d bytes", len(big), len(small))
	}
}
