package parser_test

// Determinism guard for the parser.
//
// parser.Parse is the entry point of the whole compilation pipeline:
// every other stage (checker, modload, IR, codegen, interp) consumes
// the *ast.Program it produces. Nondeterminism at this layer would
// propagate to every downstream stage at once — strictly worse than
// the per-stage determinism guards already in place (modload, ir,
// codegen, interp), and harder to localize because it surfaces as
// simultaneous multi-stage "mismatches".
//
// Recursive descent is normally deterministic by construction, but
// the same was true of modload until TestLoadDeterministic caught a
// real Go-map-iteration leak. Pinning this here means a future
// change to the parser that uses a Go map to drive output order
// (e.g. an interning table whose iteration ends up in the AST)
// fails loudly at the source, not as a flaky downstream diff.
//
// The witness is printer.Print — it serialises function / struct /
// enum order + every body, so byte-equal output across repeated
// parses of the same source is a faithful indicator of an
// identical AST (modulo pointer addresses, which printer ignores).

import (
	"testing"

	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/printer"
)

// determinismMatrix favours surfaces that would be most prone to
// ordering nondeterminism if the parser ever maintained interning
// tables, dedup maps, or similar: many functions (declaration
// order), several distinct string literals (per-literal data
// records), structs + enums (decl tables), f-strings (multi-part
// emit), and a representative real-world spread from std-style
// code.
var determinismMatrix = map[string]string{
	"minimal": `function main(): i32 { return 0; }`,

	"many_funcs_strings": `
function a(): string { return "alpha"; }
function b(): string { return "beta"; }
function c(): string { return "gamma"; }
function main(): i32 {
	print(a()); print(b()); print(c());
	return 0;
}`,

	"struct_enum_mix": `
struct Point { x: i32, y: i32 }
enum Shape { Circle(i32), Rect(i32, i32) }
function area(s: Shape): i32 {
	match (s) {
		Circle(r) => { return r * r; },
		Rect(w, h) => { return w * h; }
	}
	return 0;
}
function main(): i32 {
	var p: Point = Point { x: 3, y: 4 };
	return area(Circle(p.x));
}`,

	"fstring_parts": `
function main(): i32 {
	var name: string = "world";
	var n: i32 = 42;
	print(f"hello, {name}, value={n}, end");
	return 0;
}`,

	"control_flow": `
function classify(n: i32): i32 {
	if (n > 0) { return 1; } else if (n < 0) { return -1; }
	return 0;
}
function sum_to(n: i32): i32 {
	var s: i32 = 0;
	var i: i32 = 0;
	while (i <= n) { s = s + i; i = i + 1; }
	return s;
}
function main(): i32 {
	return classify(-5) + classify(5) + classify(0) + sum_to(10);
}`,
}

// TestParseDeterministic parses each program several times and asserts
// every parse produces a printer-byte-identical Program. A failure
// means the parser's AST output depends on some non-deterministic
// process state (most likely Go map iteration order, but pointer
// hash dependence and runtime.GOMAXPROCS-influenced goroutine order
// would also surface here).
func TestParseDeterministic(t *testing.T) {
	for name, src := range determinismMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			first := mustParsePrint(t, src)
			for i := 0; i < 4; i++ {
				again := mustParsePrint(t, src)
				if again != first {
					t.Fatalf("parser not deterministic on run %d: output differs (%d bytes vs %d bytes)",
						i+2, len(first), len(again))
				}
			}
		})
	}
}

// mustParsePrint parses src and returns the printed form of the
// resulting Program. Fails the test on any parse error.
func mustParsePrint(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return printer.Print(prog)
}
