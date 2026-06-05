package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// inferParamEscapes is the foundation analysis for ownership / borrow inference
// (Slice 0 of docs/OWNERSHIP-INFERENCE-PLAN.md): per pointer parameter, does its
// heap value escape the function. A NON-escaping param is reclaim-safe. A false
// "non-escaping" would later become a use-after-free once it drives reclaim, so
// pin both directions — including the precision the call-graph fixpoint and the
// match-binding / var taint buy.
func TestInferParamEscapes(t *testing.T) {
	prog, err := parser.Parse(`enum List { Cons(i32, List), Nil }
struct Box { inner: List }
function sum(l: List): i32 { match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } } }
function eat(own xs: List): i32 { match (xs) { Cons(h, t) => { return h + eat(t); }, Nil => { return 0; } } }
function dup(xs: List): List { match (xs) { Cons(h, t) => { return Cons(h, dup(t)); }, Nil => { return Nil; } } }
function reads(xs: List): i32 { var c: i32 = sum(xs); return c; }
function viadup(xs: List): List { var r: List = dup(xs); return r; }
function ident(xs: List): List { return xs; }
function prepend(x: i32, xs: List): List { return Cons(x, xs); }
function tailof(xs: List): List { match (xs) { Cons(h, t) => { return t; }, Nil => { return Nil; } } }
function wrapbox(xs: List): Box { return Box { inner: xs }; }
function bindret(xs: List): List { var r: List = xs; return r; }
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	esc := inferParamEscapes(prog, info)

	// fn -> (param index, expected-escapes)
	cases := []struct {
		fn     string
		idx    int
		escape bool
		why    string
	}{
		{"sum", 0, false, "only read (match scrutinee + recursion through a borrowing call)"},
		{"eat", 0, false, "consumed/freed but never flows out — reclaim-safe"},
		{"dup", 0, false, "self-recursive fresh map; param never reaches the result"},
		{"reads", 0, false, "passed to sum (borrows) then a scalar is returned"},
		{"viadup", 0, false, "dup borrows its arg, so the bound result isn't param-derived"},
		{"ident", 0, true, "returns the param"},
		{"prepend", 1, true, "Cons(x, xs) — xs escapes into the tail"},
		{"tailof", 0, true, "returns a match binding (a projection of the param)"},
		{"wrapbox", 0, true, "Box{inner: xs} — xs escapes into a field"},
		{"bindret", 0, true, "var r = xs; return r — escapes via the alias"},
	}
	for _, c := range cases {
		got := false
		if e := esc[c.fn]; c.idx < len(e) {
			got = e[c.idx]
		}
		if got != c.escape {
			t.Errorf("inferParamEscapes[%s][%d] = %v, want %v (%s)", c.fn, c.idx, got, c.escape, c.why)
		}
	}
}
