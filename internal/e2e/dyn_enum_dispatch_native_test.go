// Native-backend coverage for ENUM values dispatched behind `dyn Trait`
// (#4785's native leg). The native backends pack a `dyn` value as a boxed
// {data, vtable} cell at the coercion site, so an enum local coerced into a
// dyn slot dispatches through its vtable — these pin that the shapes fixed
// on the self-host IR path (internal/e2eselfhost/self_host_dyn_enum_ir_test.go,
// same case sources) keep working natively. The heterogeneous struct+enum
// `dyn Trait[]` LOCAL-element shape is deliberately absent here: the native
// x86-64 backend segfaults on it today — tracked as #4787.
package e2e

import "testing"

var dynEnumNativeCases = []struct {
	name     string
	src      string
	expected int
}{
	{"enum-local-coerce",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } function go(k: i32): i32 { var e: Op = Add(k); var d: dyn Show = e; return d.show(); } function main(): i32 { return go(3); }`, 4},
	{"enum-direct-init",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } function main(): i32 { var d: dyn Show = Add(41); return d.show(); }`, 42},
	{"enum-local-to-param",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } function run(s: dyn Show): i32 { return s.show(); } function main(): i32 { var e: Op = Add(9); return run(e); }`, 10},
	{"enum-unit-variant",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 7; } } } } function go(): i32 { var e: Op = Neg; var d: dyn Show = e; return d.show(); } function main(): i32 { return go(); }`, 7},
	{"enum-method-arg",
		`trait Sc { function sc(self: Self, k: i32): i32; } enum Op { Add(i32), Neg } impl Sc for Op { function sc(self: Self, k: i32): i32 { match (self) { Add(v) => { return v * k; }, Neg => { return 0 - k; } } } } function f(s: dyn Sc): i32 { return s.sc(3); } function main(): i32 { var e: Op = Add(5); return f(e); }`, 15},
	{"enum-two-enums",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } enum Col { Red, Blue } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } impl Show for Col { function show(self: Self): i32 { match (self) { Red => { return 10; }, Blue => { return 20; } } } } function run(s: dyn Show): i32 { return s.show(); } function main(): i32 { var a: Op = Add(3); var b: Col = Blue; return run(a) + run(b); }`, 24},
}

// TestX86_64NativeDynEnumDispatch runs each case through the x86-64
// codegen + pure-Go assembler/linker and asserts the exit code.
func TestX86_64NativeDynEnumDispatch(t *testing.T) {
	for _, tc := range dynEnumNativeCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86Native(t, tc.src); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
