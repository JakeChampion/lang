package interp

// Determinism guard for the tree-walking interpreter.
//
// The interpreter is the differential oracle: TestDifferential_LangsmithMain
// (internal/e2e) compares every backend's output against the value the
// interpreter produces for the same source. That comparison is only
// sound if the interpreter is itself deterministic — if a program's
// interpreted result or its stdout could vary run-to-run, the oracle
// would emit spurious backend "mismatches" (or, worse, mask real ones
// when the oracle happens to drift the same way). So determinism here
// underpins the whole fuzzing strategy.
//
// The interpreter holds program state in Go maps (struct fields,
// Map values, function/enum tables, environment frames), and map
// iteration order is randomized — so anything that observes a map in
// order (printing a struct, iterating a Map, formatting for output)
// is a place nondeterminism could leak into either the result Value
// or the captured stdout. These tests run each program in a fresh
// interpreter several times and assert both the returned value and
// the stdout bytes are identical across runs.

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// runCapture parses, checks, registers, and runs main in a fresh
// interpreter, returning main's value rendered as a stable string
// plus everything written to stdout. A fresh interpreter per call is
// deliberate: it ensures run-to-run identity comes from deterministic
// evaluation, not from shared accumulated state.
func runCapture(t *testing.T, src string) (val string, stdout string) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	i := New()
	var buf bytes.Buffer
	i.Stdout = &buf
	for _, ed := range prog.Enums {
		i.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	v, err := i.CallByName("main", nil)
	if err != nil {
		t.Fatalf("call main: %v\nsrc:\n%s", err, src)
	}
	// Render via %v: the Value types are concrete (Number, String,
	// …) with stable formatting, so this is a deterministic witness
	// of the result regardless of the underlying Go type.
	return fmt.Sprintf("%v", v), buf.String()
}

// determinismMatrix favours the map-backed evaluation paths most
// exposed to Go map-iteration-order nondeterminism: struct field
// access, Map insertion/iteration, closures (captured-env frames),
// and string building from iterated collections. Each is
// self-contained (defines main, no imports).
var determinismMatrix = map[string]string{
	"arithmetic": `
function main(): i32 {
	var x: i32 = 6;
	var y: i32 = 7;
	return x * y + (y - x) / 2;
}`,

	"struct_fields": `
struct Point { x: i32, y: i32 }
function (p: Point) sum(): i32 { return p.x + p.y; }
function main(): i32 {
	var p: Point = Point { x: 3, y: 4 };
	return p.sum();
}`,

	"map_iteration": `
function main(): i32 {
	var m: Map[string, i32] = Map { "a": 1, "b": 2, "c": 3 };
	var total: i32 = 0;
	for (k, v) in m { total = total + v; }
	return total;
}`,

	"map_print_order": `
function main(): void {
	var m: Map[string, i32] = Map { "a": 1, "b": 2, "c": 3 };
	for (k, v) in m { print(k); }
}`,

	"closures": `
function adder(n: i32): (i32) => i32 {
	return function (x: i32): i32 { return x + n; };
}
function main(): i32 {
	var f: (i32) => i32 = adder(10);
	var g: (i32) => i32 = adder(20);
	return f(1) + g(2);
}`,

	"string_build": `
function main(): void {
	var xs: string[] = ["a", "b", "c", "d"];
	var out: string = "";
	for x in xs { out = out + x; }
	print(out);
}`,

	"recursion": `
function fact(n: i32): i32 {
	if (n == 0) { return 1; }
	return n * fact(n - 1);
}
function main(): i32 { return fact(6); }`,
}

// TestInterpDeterministic runs each program several times in fresh
// interpreters and asserts both the returned value and the stdout
// bytes match the first run. A failure means interpreter
// nondeterminism (most likely map-iteration order observed during
// evaluation or printing) has leaked into the oracle — which would
// undermine the differential-testing strategy every backend relies on.
func TestInterpDeterministic(t *testing.T) {
	for name, src := range determinismMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			wantVal, wantOut := runCapture(t, src)
			for i := 0; i < 8; i++ {
				gotVal, gotOut := runCapture(t, src)
				if gotVal != wantVal {
					t.Fatalf("result not deterministic on run %d: got %q, first run %q", i+2, gotVal, wantVal)
				}
				if gotOut != wantOut {
					t.Fatalf("stdout not deterministic on run %d: got %q, first run %q", i+2, gotOut, wantOut)
				}
			}
		})
	}
}
