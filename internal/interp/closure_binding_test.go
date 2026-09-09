package interp

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

func TestClosureBindingIdentity(t *testing.T) {
	cases := []struct {
		name, src string
		want      Number
	}{
		{"later declaration", `function main(): i32 {
var n = 7; var call = (): i32 => {
var inner = (): i32 => n; var n = 99; return inner(); }; return call(); }`, 7},
		{"local function", `function main(): i32 {
var n = 7; if (true) { function inner(): i32 { return n; }
var n = 99; return inner(); } return 99; }`, 7},
		{"original binding remains mutable", `function main(): i32 {
var n = 1; var call = (): i32 => n; n = 7; return call(); }`, 7},
		{"write through original binding", `function main(): i32 {
var n = 1; if (true) { var put = (): void => { n = 7; };
var n = 99; put(); if (n != 99) { return 98; } } return n; }`, 7},
		{"recursive local function", `function main(): i32 {
function rec(n: i32): i32 { if (n == 0) { return 7; } return rec(n - 1); }
return rec(3); }`, 7},
		{"escaping mutable binding", `function make(): () => i32 {
var n = 0; return (): i32 => { n = n + 1; return n; }; }
function main(): i32 { var f = make(); return f() * 10 + f(); }`, 12},
		{"escaping array binding", `function make(xs: i32[]): () => i32[] {
return (): i32[] => xs; }
function main(): i32 { var xs = [1]; var f = make(xs); xs = xs.with(0, 9);
var original = f(); return original[0] * 10 + xs[0]; }`, 19},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := evalProgram(t, tc.src)
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCapturedEnvironmentBindings(t *testing.T) {
	outer := newEnv(nil)
	outer.declare("n", Number(1))
	inner := newEnv(outer)
	before := inner.capture([]ast.Param{{Name: "n"}})
	inner.declare("n", Number(99))
	outer.set("n", Number(7))
	if got, _ := before.get("n"); got != Number(7) {
		t.Fatalf("capture no longer denotes original mutable binding: %v", got)
	}
	before.set("n", Number(8))
	if got, _ := outer.get("n"); got != Number(8) {
		t.Fatalf("capture assignment did not update original binding: %v", got)
	}
	outer.declare("n", Number(42))
	if got, _ := before.get("n"); got != Number(8) {
		t.Fatalf("redeclaration replaced captured binding identity: %v", got)
	}
	inner.declare("later", Number(3))
	if _, found := before.get("later"); found {
		t.Fatal("capture gained a later declaration")
	}
	empty := inner.capture([]ast.Param{})
	if got := len(empty.vars); got != 0 {
		t.Fatalf("checked capture-free environment has %d bindings", got)
	}
	unchecked := inner.capture(nil)
	inner.declare("future", Number(4))
	if _, found := unchecked.get("future"); found {
		t.Fatal("unchecked capture gained a later declaration")
	}
}
