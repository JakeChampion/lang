package e2ecompiler

import "testing"

var genericDefaultIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"box-i32",
		`trait Default { function default(): Self; } impl Default for i32 { function default(): i32 { return 0; } } @derive(Default) struct Box[T] { v: T } function main(): i32 { let b: Box[i32] = Box.default(); return b.v + 7; }`, 7},
	{"box-string",
		`trait Default { function default(): Self; } impl Default for string { function default(): string { return ""; } } @derive(Default) struct Box[T] { v: T, k: i32 } function main(): i32 { let b: Box[string] = Box.default(); return b.v.len() + b.k + 4; }`, 4},
	{"box-boolean",
		`trait Default { function default(): Self; } impl Default for boolean { function default(): boolean { return false; } } @derive(Default) struct Box[T] { v: T, k: i32 } function main(): i32 { let b: Box[boolean] = Box.default(); if (b.v) { return 1; } return b.k + 8; }`, 8},
	{"box-inner",
		`trait Default { function default(): Self; } @derive(Default) struct Inner { n: i32 } @derive(Default) struct Box[T] { v: T } function main(): i32 { let b: Box[Inner] = Box.default(); return b.v.n + 5; }`, 5},
	{"two-field",
		`trait Default { function default(): Self; } @derive(Default) struct Inner { n: i32 } @derive(Default) struct Pair[T] { a: T, b: i32 } function main(): i32 { let p: Pair[Inner] = Pair.default(); return p.a.n + p.b + 9; }`, 9},
	{"two-instantiations",
		`trait Default { function default(): Self; } @derive(Default) struct A { n: i32 } @derive(Default) struct B { m: i32 } @derive(Default) struct Box[T] { v: T } function main(): i32 { let x: Box[A] = Box.default(); let y: Box[B] = Box.default(); return x.v.n + y.v.m + 12; }`, 12},
	{"multi-field-inner",
		`trait Default { function default(): Self; } @derive(Default) struct Pt { x: i32, y: i32, tag: string } @derive(Default) struct Box[T] { v: T } function main(): i32 { let b: Box[Pt] = Box.default(); return b.v.x + b.v.y + b.v.tag.len() + 15; }`, 15},
}

func TestSelfHostGenericDefaultIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range genericDefaultIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
