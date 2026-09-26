package e2eselfhost

import "testing"

// genericDefaultIRCases exercise `@derive(Default)` on a GENERIC struct —
// `Box.default()` instantiated as `Box[Inner]` — through the stack-IR path.
// This is the generic case of associated-function dispatch (#2779 item 3): the
// synthesized `default()` body builds `Box { v: T.default() }`, and the
// monomorphiser must (a) clone the receiver-less associated method per
// instantiation, (b) substitute `T.default()` → `Inner.default()` in the cloned
// body (subst_expr), (c) infer the struct literal's instantiation from the
// defaulted field value (mono_infer of an associated call), and (d) retarget the
// call site `Box.default()` → `Box__Inner.default()` from the binding's
// annotation (a receiver-less constructor has no args to infer from).
//
// Scope: type params instantiated with a leaf-safe STRUCT or a PRIMITIVE. A
// primitive param (`Box[i32]`) needs a primitive `Default` impl in scope for
// the native compiler (#2864); the self-host substitutes the primitive's zero
// LITERAL for `T.default()` directly. An enum-typed field isn't leaf-safe in
// the IR struct model — left as a follow-up. The inline `trait Default` (+
// primitive impl, where needed) keeps each program valid for both compilers.
var genericDefaultIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Primitive type params: the self-host emits the zero literal for the
	// defaulted field; the native compiler dispatches through the primitive
	// `Default` impl. Both yield the same result.
	{"box-i32",
		`trait Default { function default(): Self; } impl Default for i32 { function default(): i32 { return 0; } } @derive(Default) struct Box[T] { v: T } function main(): i32 { var b: Box[i32] = Box.default(); return b.v + 7; }`, 7},
	{"box-string",
		`trait Default { function default(): Self; } impl Default for string { function default(): string { return ""; } } @derive(Default) struct Box[T] { v: T, k: i32 } function main(): i32 { var b: Box[string] = Box.default(); return b.v.len() + b.k + 4; }`, 4},
	{"box-boolean",
		`trait Default { function default(): Self; } impl Default for boolean { function default(): boolean { return false; } } @derive(Default) struct Box[T] { v: T, k: i32 } function main(): i32 { var b: Box[boolean] = Box.default(); if (b.v) { return 1; } return b.k + 8; }`, 8},
	// Box[Inner]: the type param defaults to a nested struct's own default. 5.
	{"box-inner",
		`trait Default { function default(): Self; } @derive(Default) struct Inner { n: i32 } @derive(Default) struct Box[T] { v: T } function main(): i32 { var b: Box[Inner] = Box.default(); return b.v.n + 5; }`, 5},
	// Generic field mixed with a concrete field. 0 + 0 + 9 = 9.
	{"two-field",
		`trait Default { function default(): Self; } @derive(Default) struct Inner { n: i32 } @derive(Default) struct Pair[T] { a: T, b: i32 } function main(): i32 { var p: Pair[Inner] = Pair.default(); return p.a.n + p.b + 9; }`, 9},
	// Two distinct instantiations of the same generic struct in one program. 12.
	{"two-instantiations",
		`trait Default { function default(): Self; } @derive(Default) struct A { n: i32 } @derive(Default) struct B { m: i32 } @derive(Default) struct Box[T] { v: T } function main(): i32 { var x: Box[A] = Box.default(); var y: Box[B] = Box.default(); return x.v.n + y.v.m + 12; }`, 12},
	// The instantiating struct has several fields, all defaulted. 15.
	{"multi-field-inner",
		`trait Default { function default(): Self; } @derive(Default) struct Pt { x: i32, y: i32, tag: string } @derive(Default) struct Box[T] { v: T } function main(): i32 { var b: Box[Pt] = Box.default(); return b.v.x + b.v.y + b.v.tag.len() + 15; }`, 15},
}

// TestSelfHostGenericDefaultIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
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
