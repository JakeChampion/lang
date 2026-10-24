package e2ecompiler

import "testing"

var arrayFieldSetCases = []struct {
	name string
	src  string
	exit int
}{
	{
		"set-fresh-literal",
		`struct S { code: i32[], n: i32 }
function main(): i32 { let s: S = S { code: [], n: 0 }; s = S { ...s, code: [1, 2] }; return s.code.len(); }`,
		2,
	},
	{
		"set-scalar-field",
		`struct S { code: i32[], n: i32 }
function main(): i32 { let s: S = S { code: [1], n: 0 }; s = S { ...s, n: 5 }; return s.n + s.code.len(); }`,
		6,
	},
	{
		"set-aliased-local",
		`struct S { code: i32[], n: i32 }
function main(): i32 { let s: S = S { code: [], n: 0 }; let o: i32[] = [7, 8, 9]; s = S { ...s, code: o }; return s.code[1] + o[2]; }`,
		17,
	},
	{
		"set-in-loop",
		`struct S { code: i32[], n: i32 }
function main(): i32 {
    let s: S = S { code: [], n: 0 };
    let i: i32 = 0;
    while (i < 50) { s = S { ...s, code: [i, i + 1] }; i = i + 1; }
    return s.code[0] + s.code[1];
}`,
		99,
	},
	{
		"set-struct-array-field",
		`struct P { x: i32 }
struct T { items: P[], n: i32 }
function main(): i32 { let t: T = T { items: [], n: 0 }; t = T { ...t, items: [P { x: 4 }, P { x: 5 }] }; return t.items[0].x + t.items[1].x; }`,
		9,
	},
}

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
