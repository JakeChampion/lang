package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// findReturnsNoParamEscape is the safety oracle behind reclaiming an owned
// temporary passed to a POINTER-returning callee (the nested-call leak fix). A
// false positive here is a use-after-free, so pin both directions: fresh
// returners qualify, identity / projection / param-embedding returners do not.
func TestReturnsNoParamEscape(t *testing.T) {
	prog, err := parser.Parse(`enum List { Cons(i32, List), Nil }
struct Box { inner: List }
function build(n: i32): List { if (n == 0) { return Nil; } return Cons(n, build(n - 1)); }
function dup(xs: List): List { match (xs) { Cons(h, t) => { return Cons(h, dup(t)); }, Nil => { return Nil; } } }
function (xs: List) mdup(): List { match (xs) { Cons(h, t) => { return Cons(h, t.mdup()); }, Nil => { return Nil; } } }
function ident(xs: List): List { return xs; }
function prepend(x: i32, xs: List): List { return Cons(x, xs); }
function wrapbox(xs: List): Box { return Box { inner: xs }; }
function freshbox(n: i32): Box { return Box { inner: build(n) }; }
function viacall(xs: List): List { return dup(xs); }
function viaident(xs: List): List { return ident(xs); }
function pick(a: List, b: List): List { return b; }
function scalarret(xs: List): i32 { match (xs) { Cons(h, t) => { return h; }, Nil => { return 0; } } }
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	q := findReturnsNoParamEscape(prog, info)

	want := map[string]bool{
		"build":              true,  // Nil / Cons(scalar, build(..))
		"dup":                true,  // self-recursive fresh map
		"__method_List_mdup": true,  // method form of dup
		"freshbox":           true,  // Box{inner: build(n)} — fresh all the way down
		"viacall":            true,  // returns dup(xs) — dup qualifies, so result is fresh
		"scalarret":          true,  // scalar result can't carry a param's heap
		"ident":              false, // returns the param
		"prepend":            false, // Cons(x, xs) — xs (a param) escapes into the tail
		"wrapbox":            false, // Box{inner: xs} — xs escapes into the field
		"viaident":           false, // returns ident(xs); ident escapes, so this does too
		"pick":               false, // returns a param
	}
	for name, exp := range want {
		if q[name] != exp {
			t.Errorf("returnsNoParamEscape[%s] = %v, want %v", name, q[name], exp)
		}
	}
}

// Fresh-local returns: `return r` where r was built locally qualifies, while a
// local that aliases a parameter or is exposed to mutation does not. A false
// positive here is a use-after-free, so pin both directions.
func TestReturnsNoParamEscapeFreshLocals(t *testing.T) {
	prog, err := parser.Parse(`enum List { Cons(i32, List), Nil }
function build(n: i32): List { if (n == 0) { return Nil; } return Cons(n, build(n - 1)); }
function sum(l: List): i32 { match (l) { Cons(h, t) => { return h; }, Nil => { return 0; } } }
function freshlit(): List { var r: List = Cons(0, Nil); return r; }
function freshcall(n: i32): List { var r: List = build(n); return r; }
function embedfresh(n: i32): List { var r: List = build(n); return Cons(1, r); }
function chainfresh(n: i32): List { var a: List = build(n); var b: List = build(n); return Cons(0, b); }
function retparam(xs: List): List { var r: List = xs; return r; }
function passthenret(xs: List): List { var r: List = build(2); var u: i32 = sum(r); return r; }
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	q := findReturnsNoParamEscape(prog, info)
	want := map[string]bool{
		"freshlit":    true,  // var r = Cons(0, Nil); return r
		"freshcall":   true,  // var r = build(n); return r
		"embedfresh":  true,  // var r = build(n); return Cons(1, r)
		"chainfresh":  true,  // return Cons(0, b) where b is fresh
		"retparam":    false, // var r = xs; return r — r aliases the param
		"passthenret": false, // r passed to a call before return — exposed to mutation
	}
	for name, exp := range want {
		if q[name] != exp {
			t.Errorf("returnsNoParamEscape[%s] = %v, want %v", name, q[name], exp)
		}
	}
}
