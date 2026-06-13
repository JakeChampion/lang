package e2e

import "testing"

// TestNativeMutScalarCapture proves the NATIVE compiled pipeline
// (internal/closureconv → internal/ir → codegen) implements MUTABLE SCALAR
// CAPTURES by reference (#2896): a closure that writes a captured outer
// i32/bool/f64 shares the write with the enclosing scope (closures as
// counters). The native pipeline previously captured every scalar BY VALUE, so
// the write was lost (repro → 8 not 49; counter → 0 not 2). The fix boxes
// captured-and-mutated scalars into 1-element array cells
// (closureconv.BoxMutatedScalarCaptures) so the existing array-pointer capture
// is by-reference — mirroring the self-host IR fix (#2895) and matching the Go
// reference interpreter, which defines the semantics.
//
// Run on x86-64, arm64 (qemu), and the wasmbin core-module path for full
// backend parity. Expected values are the interpreter's; read-only captures
// stay by-value (not boxed) and the read-only case guards that path is
// unchanged.
func TestNativeMutScalarCapture(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		// Write-only: the lambda writes the captured scalar without reading it.
		// By-value → write lost (8); by-reference → 7 + 42 = 49.
		{"write-only", `function main(): i32 { var x = 1; var f = function (): i32 { x = 42; return 7; }; var r = f(); return r + x; }`, 49},
		// Counter: read+write capture accumulates across two calls → 2.
		{"counter", `function main(): i32 { var x = 0; var inc = function (): i32 { x = x + 1; return x; }; var a = inc(); var b = inc(); return x; }`, 2},
		// Counter taking a param, mutating a captured local across two calls → 18.
		{"counter-param", `function main(): i32 { var n = 10; var add = function (d: i32): i32 { n = n + d; return n; }; var a = add(5); var b = add(3); return n; }`, 18},
		// The lambda's own return reflects the post-write value → 5*2 = 10.
		{"returns-written", `function main(): i32 { var x = 5; var f = function (): i32 { x = x * 2; return x; }; return f(); }`, 10},
		// Read-only capture is NOT boxed — stays by-value; must still work → 6.
		{"read-only", `function main(): i32 { var x = 5; var f = function (): i32 { return x + 1; }; return f(); }`, 6},
		// A boolean captured scalar, toggled in the closure → 1.
		{"bool-capture", `function main(): i32 { var b = false; var t = function (): i32 { b = true; return 0; }; var r = t(); if (b) { return 1; } return 0; }`, 1},
		// An f64 captured scalar mutated in the closure (8-byte cell stride) → 49.
		{"f64-capture", `function main(): i32 { var x: f64 = 1.0; var f = function (): i32 { x = 42.0; return 7; }; var r = f(); if (x > 41.0) { return 49; } return r; }`, 49},
		// Two closures sharing one boxed cell: writer then reader observes it → 9.
		{"shared-cell", `function main(): i32 { var x = 0; var setter = function (): i32 { x = 4; return 0; }; var getter = function (): i32 { return x + 5; }; var a = setter(); return getter(); }`, 9},
		// A boxed counter driven inside a loop → 3.
		{"loop-counter", `function main(): i32 { var x = 0; var inc = function (): i32 { x = x + 1; return 0; }; var i = 0; while (i < 3) { var r = inc(); i = i + 1; } return x; }`, 3},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Run("x86_64", func(t *testing.T) {
				if _, got := compileAndRunX86_64(t, tc.src); got != tc.want {
					t.Errorf("x86-64 %q: exit = %d, want %d", tc.name, got, tc.want)
				}
			})
			t.Run("arm64", func(t *testing.T) {
				if _, got := compileAndRunArm64(t, tc.src); got != tc.want {
					t.Errorf("arm64 %q: exit = %d, want %d", tc.name, got, tc.want)
				}
			})
			t.Run("wasm", func(t *testing.T) {
				if got := compileAndRunWasmbinMain(t, tc.src); got != tc.want {
					t.Errorf("wasm %q: exit = %d, want %d", tc.name, got, tc.want)
				}
			})
		})
	}
}
