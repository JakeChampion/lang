package e2ecompiler

import (
	"testing"
)

// nestedTupleCaptureIRCases exercise a lambda capturing a binding produced by a
// NESTED tuple destructure — a first destructure binds a name to a tuple
// (pointer) element, a second destructure splits that — through the self-host IR
// path on x86-64.
//
// A first-level destructure whose element is itself a tuple (`let (p, c) = t`
// with t : ((i32,i32), i32), so p : (i32,i32)) gives p a tuple type the lift
// can read, so the second-level `let (a, b) = p` resolves and the capture of
// a/b — or a direct capture of p — lifts (#5201). The cases use all-i32 tuples.
//
// Each case is oracle-checked against the interpreter; results stay <= 120 (the wasm exit-code clamp, #2908).
var nestedTupleCaptureIRCases = []struct {
	name string
	main string
}{
	// The #5201 repro: two-level all-i32 nesting, innermost bindings captured in
	// a struct fn-field lambda. h.f(1)=1+3+4+5=13, h.id=a=3 → 16.
	{"nested-2level", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
	let t: ((i32, i32), i32) = ((3, 4), 5);
	let (p, c) = t;
	let (a, b) = p;
	let h: H = H { f: (x: i32): i32 => { return x + a + b + c; }, id: a };
	return h.f(1) + h.id;
}
function main(): i32 { return g(); }`},
	// Direct capture of the intermediate tuple binding p (a (i32,i32) pointer
	// stored in the 32-bit env slot). h.f(1)=1+3+4+5=13, h.id=c=5 → 18.
	{"direct-tuple-capture", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
	let t: ((i32, i32), i32) = ((3, 4), 5);
	let (p, c) = t;
	let h: H = H { f: (x: i32): i32 => { return x + p.0 + p.1 + c; }, id: c };
	return h.f(1) + h.id;
}
function main(): i32 { return g(); }`},
	// Three-level all-i32 nesting. h.f(1)=1+1+2+3+4=11, h.id=a=1 → 12.
	{"nested-3level", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
	let t: (((i32, i32), i32), i32) = (((1, 2), 3), 4);
	let (q, d) = t;
	let (p, c) = q;
	let (a, b) = p;
	let h: H = H { f: (x: i32): i32 => { return x + a + b + c + d; }, id: a };
	return h.f(1) + h.id;
}
function main(): i32 { return g(); }`},
}

// TestSelfHostNestedTupleCaptureIRX86_64 compiles each case with the self-host
// CLI for x86-64, oracle-checked.
func TestSelfHostNestedTupleCaptureIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range nestedTupleCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)\n%s", tc.name, code, want, stderr)
			}
		})
	}
}
