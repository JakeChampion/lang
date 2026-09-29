package e2eselfhost

import "testing"

// Replacing an ARRAY-typed struct field through a functional update
// (`s = S { ...s, code: [1, 2] }`). A new value that aliases a live array local
// is shared with it, so reading through both after the update must see live
// data; the replaced buffer must not be the one read back. Every exit code
// matches bin/fern -interp.
var arrayFieldSetCases = []struct {
	name string
	src  string
	exit int
}{
	{
		// The reduced repro: a fresh array literal replaces an empty one.
		"set-fresh-literal",
		`struct S { code: i32[], n: i32 }
function main(): i32 { var s: S = S { code: [], n: 0 }; s = S { ...s, code: [1, 2] }; return s.code.len(); }`,
		2,
	},
	{
		// Control: the SCALAR field of the same struct.
		"set-scalar-field",
		`struct S { code: i32[], n: i32 }
function main(): i32 { var s: S = S { code: [1], n: 0 }; s = S { ...s, n: 5 }; return s.n + s.code.len(); }`,
		6,
	},
	{
		// The retain case. `o` is an array local stored into the field, so both
		// own the buffer; reading through BOTH after the update must see live
		// data.
		"set-aliased-local",
		`struct S { code: i32[], n: i32 }
function main(): i32 { var s: S = S { code: [], n: 0 }; var o: i32[] = [7, 8, 9]; s = S { ...s, code: o }; return s.code[1] + o[2]; }`,
		17,
	},
	{
		// Repeated replacement in a loop: the value read must be the LAST one
		// stored.
		"set-in-loop",
		`struct S { code: i32[], n: i32 }
function main(): i32 {
    var s: S = S { code: [], n: 0 };
    var i: i32 = 0;
    while (i < 50) { s = S { ...s, code: [i, i + 1] }; i = i + 1; }
    return s.code[0] + s.code[1];
}`,
		99,
	},
	{
		// A STRUCT-element array field.
		"set-struct-array-field",
		`struct P { x: i32 }
struct T { items: P[], n: i32 }
function main(): i32 { var t: T = T { items: [], n: 0 }; t = T { ...t, items: [P { x: 4 }, P { x: 5 }] }; return t.items[0].x + t.items[1].x; }`,
		9,
	},
}

// TestSelfHostArrayFieldSetIRWasm runs each case through the self-hosted CLI
// for wasm32-wasi.
func TestSelfHostArrayFieldSetIRWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range arrayFieldSetCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostArrayFieldSetIRX86_64 is the same programs for x86-64.
func TestSelfHostArrayFieldSetIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range arrayFieldSetCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s: exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
