package e2ecompiler

import (
	"testing"
)

// nestedTupleDestructureIRCases exercise a NESTED tuple destructure — a first
// destructure binds a name to a tuple (pointer) element, a second destructure
// (or a `.N` read) splits that — through the self-host IR path on x86-64.
//
// The destructure lowering marked a bound slot's element kind (string / i64 /
// array / struct / …) for scalar and pointer LEAF elements, but had no branch
// for a nested-TUPLE element: `let (p, c) = t` with t : ((string,i32), i32)
// bound p (a (string,i32) pointer) with NO tuple_elems, so the second-level
// `let (s, b) = p` resolved s's tag as "" and read it via the untyped
// op_tuple_get — a pointer element (string) came through empty (#5306 gap 1).
// The fix records the nested tuple's element tags on p's slot, so the second
// destructure (and `p.N`) resolve each element's type. i32 nested elements were
// already correct (they need no per-element mark); this closes the pointer case.
//
// Each case is oracle-checked against the interpreter; results stay <= 120 (the wasm exit-code clamp, #2908).
var nestedTupleDestructureIRCases = []struct {
	name string
	main string
}{
	// Nested string element via a second destructure: 2 + 4 + 5 = 11 (was 9 — s
	// came through empty).
	{"nested-str-2level", `function g(): i32 {
	let t: ((string, i32), i32) = (("hi", 4), 5);
	let (p, c) = t;
	let (s, b) = p;
	return s.len() + b + c;
}
function main(): i32 { return g(); }`},
	// Nested string element read via `.N` on the intermediate binding: 3+4+5 = 12.
	{"nested-str-dotN", `function g(): i32 {
	let t: ((string, i32), i32) = (("hey", 4), 5);
	let (p, c) = t;
	return p.0.len() + p.1 + c;
}
function main(): i32 { return g(); }`},
	// Three-level nesting with a string at the bottom: 2 + 1 + 2 + 3 = 8.
	{"nested-str-3level", `function g(): i32 {
	let t: (((string, i32), i32), i32) = ((("ab", 1), 2), 3);
	let (q, d) = t;
	let (p, c) = q;
	let (s, b) = p;
	return s.len() + b + c + d;
}
function main(): i32 { return g(); }`},
	// All-i32 nested destructure (regression baseline): 3 + 4 + 5 = 12.
	{"nested-i32-2level", `function g(): i32 {
	let t: ((i32, i32), i32) = ((3, 4), 5);
	let (p, c) = t;
	let (a, b) = p;
	return a + b + c;
}
function main(): i32 { return g(); }`},
}

// TestSelfHostNestedTupleDestructureIRX86_64 compiles each case with the
// self-host CLI for x86-64, oracle-checked.
func TestSelfHostNestedTupleDestructureIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range nestedTupleDestructureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)\n%s", tc.name, code, want, stderr)
			}
		})
	}
}
