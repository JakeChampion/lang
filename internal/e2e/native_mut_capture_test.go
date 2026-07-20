package e2e

import "testing"

// TestNativeMutScalarCapture proves the NATIVE compiled pipeline
// (internal/closureconv → internal/ir → codegen) implements MUTABLE SCALAR
// CAPTURES by reference (#2896): a captured outer i32/bool/f64 is ONE shared
// cell, so writes are visible symmetrically in BOTH directions — a closure's
// write escapes to the enclosing scope, AND an enclosing-scope write is seen by
// a closure that reads the variable. The native pipeline previously captured
// scalars BY VALUE, so the write was lost (repro → 8 not 49; counter → 0 not 2)
// and, until the #4391 follow-up, an outer-scope mutation after capture was a
// stale make-time snapshot (`outer-mutation`/`loop-outer-mutation` returned 0
// where the interpreter returned 5/10). The fix boxes captured mutable scalars
// into 1-element array cells (closureconv.BoxMutatedCaptures) — boxing on
// assignment ANYWHERE, not only inside the closure — and marks those cells so
// the IR's index-assign copy-on-write is skipped for them (a shared cell is
// never forked). This mirrors the self-host IR fix (#2895) and matches the Go
// reference interpreter, which defines the semantics.
//
// Run on x86-64, arm64 (qemu), and the wasmbin core-module path for full
// backend parity. Expected values are the interpreter's. A captured scalar that
// is never assigned anywhere is left unboxed (by-value and by-reference
// coincide); the read-only case guards that path is unchanged.
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
		// #4391 follow-up — by-reference is SYMMETRIC: an outer-scope write AFTER
		// the closure is made is seen by a closure that only READS the capture.
		// By-value-at-make-time snapshotted x=0 (returned 0); shared cell → 5.
		{"outer-mutation", `function main(): i32 { var i = 0; var f = function (): i32 { return i; }; i = 5; return f(); }`, 5},
		// A read-only capture whose value the enclosing loop keeps mutating: the
		// closure reads the live counter each call → 1+2+3+4 = 10 (was 0).
		{"loop-outer-mutation", `function main(): i32 { var s = 0; var i = 0; var add = function (): i32 { s = s + i; return 0; }; while (i < 4) { i = i + 1; add(); } return s; }`, 10},
		// Both sides mutate the same captured cell: outer sets 3, closure adds 4,
		// outer reads the shared result → 7. Guards that skipping CoW on the cell
		// keeps the outer write and the closure write on the SAME buffer.
		{"outer-and-inner", `function main(): i32 { var x = 0; var f = function (): i32 { x = x + 4; return 0; }; x = 3; f(); return x; }`, 7},
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

// TestNativeMutPointerCapture is the POINTER-typed sibling of
// TestNativeMutScalarCapture (#5301): a captured outer array / string / struct
// local reassigned in the ENCLOSING scope after the closure is created must be
// seen by the closure (the interpreter — the oracle per #2896 — captures by
// reference). E049 keeps the closure side read-only, so unlike scalars only the
// outer→closure direction exists; boxcapture now boxes such pointer locals into
// the same 1-element shared cell (boxableCapture admits ast.IsPointerType).
// The aliasing rows guard the RC discipline: storing a still-live local's
// pointer into the cell and later re-reading BOTH bindings must not
// over-release (__rc_underflow_count() == 0 is asserted via the exit value —
// any underflow or wrong read returns 100).
func TestNativeMutPointerCapture(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		// Array reassigned after capture: closure reads the NEW buffer → 42.
		{"array-reassign", `function main(): i32 { var a: i32[] = [10, 1]; var f: () => i32 = function (): i32 { return a[0]; }; a = [42, 1]; return f(); }`, 42},
		// String reassigned after capture: closure sees the new length → 6+36.
		{"string-reassign", `function main(): i32 { var s: string = "aa"; var f: () => i32 = function (): i32 { return s.len(); }; s = "abcdef"; return f() + 36; }`, 42},
		// Struct reassigned after capture: closure reads the new field → 42.
		{"struct-reassign", `struct B { v: i32 } function main(): i32 { var b: B = B { v: 10 }; var f: () => i32 = function (): i32 { return b.v; }; b = B { v: 42 }; return f(); }`, 42},
		// Struct with a HEAP field reassigned twice: deep shape survives → 42.
		{"struct-heap-field", `struct B { name: string, v: i32 } function main(): i32 { var b: B = B { name: "aa", v: 10 }; var f: () => i32 = function (): i32 { return b.v + b.name.len(); }; b = B { name: "abcd", v: 20 }; b = B { name: "abcdef", v: 30 }; return f() + 6; }`, 42},
		// Read-only pointer capture is NOT boxed (no reassignment anywhere) → 42.
		{"array-read-only", `function main(): i32 { var a: i32[] = [40, 2]; var f: () => i32 = function (): i32 { return a[0] + a[1]; }; return f(); }`, 42},
		// Reassign from another still-live local, then read both through closure
		// and directly — and assert zero rc underflows (over-release guard).
		{"alias-no-underflow", `function main(): i32 { var keep: i32[] = [40, 7]; var a: i32[] = [10, 1]; var f: () => i32 = function (): i32 { return a[0]; }; a = keep; a = [1, 2]; a = keep; var x: i32 = f() + keep[0]; if (x != 80) { return 100; } return __rc_underflow_count(); }`, 0},
		// Same aliasing shape for strings → 0 underflows, values intact.
		{"string-alias-no-underflow", `function main(): i32 { var keep: string = "abcdefgh"; var s: string = "aa"; var f: () => i32 = function (): i32 { return s.len(); }; s = keep; s = "abc"; s = keep; var x: i32 = f() + keep.len(); if (x != 16) { return 100; } return __rc_underflow_count(); }`, 0},
		// A loop that grows the captured string 40 times: cell stays shared → 42.
		{"loop-string-grow", `function main(): i32 { var s: string = "x"; var f: () => i32 = function (): i32 { return s.len(); }; var i: i32 = 0; while (i < 40) { s = s + "y"; i = i + 1; } return f() + 1; }`, 42},
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
