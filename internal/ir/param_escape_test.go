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
	esc := inferParamEscapes(prog, info, nil, nil)

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

// #8104: a read-only accessor that returns a SUB-OBJECT of its parameter — a
// field, or an element — does not make that parameter escape.
//
// The two branches this overrides call a projection an escape because the
// parameter's sub-heap flows out. What they do not model is that the Return
// lowering incs every rc-tracked alias on the way out, so what flows out
// carries a unit of its own and the parameter's box does not leave at all.
// Under the ownership ladder an escaping parameter is OWNED — a caller-side
// retain plus an exit drop — so the whole population of `peek` / `lookup` /
// `at` accessors was paying a transfer for a value it only read.
//
// Both directions are pinned, because the credit rests on that inc: a bare
// parameter, a fresh aggregate built around one, and a return the lowering
// rewrites before the inc is reached all keep the escape.
func TestParamEscapesCreditsAReturnedCountedProjection(t *testing.T) {
	prog, err := parser.Parse(`struct Tok { text: string, kind: i32 }
struct Par { toks: Tok[], pos: i32 }
function (p: Par) elem(): Tok { return p.toks[p.pos]; }
function (p: Par) field(): Tok[] { return p.toks; }
function (p: Par) deeper(): string { var t: Tok = p.elem(); return t.text; }
function (p: Par) whole(): Par { return p; }
function (p: Par) rebuilt(): Par { return Par { toks: p.toks, pos: p.pos + 1 }; }
function (p: Par) bound(): Tok[] { var q: Tok[] = p.toks; return q; }
function (p: Par) depth(): i32 { return p.pos; }
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		fn     string
		escape bool
		why    string
	}{
		{"__method_Par_elem", false, "returns an element — a different object, inc'd on the way out"},
		{"__method_Par_field", false, "returns the array field itself, inc'd the same way"},
		{"__method_Par_deeper", false, "the callee it reads through borrows, so nothing taints the return"},
		{"__method_Par_whole", true, "returns the parameter itself, not a projection of it"},
		{"__method_Par_rebuilt", true, "a fresh Par carrying the parameter's fields is not a projection"},
		{"__method_Par_bound", true, "the projection was bound to a local first; only a RETURN is known to inc"},
		{"__method_Par_depth", false, "a scalar return carries no heap at all"},
	}
	esc := inferParamEscapes(prog, info, nil, nil)
	for _, c := range cases {
		got := false
		if e := esc[c.fn]; len(e) > 0 {
			got = e[0]
		}
		if got != c.escape {
			t.Errorf("inferParamEscapes[%s][0] = %v, want %v (%s)", c.fn, got, c.escape, c.why)
		}
	}

	// The refusals the credit is gated on. Each rewrites the return before
	// the transfer inc is reached, so a projection returned from one is not
	// known to be counted and keeps the escape.
	for _, gate := range []struct {
		name              string
		pairForm, trmcFns map[string]bool
	}{
		{"pair-form", map[string]bool{"__method_Par_elem": true}, nil},
		{"TRMC", nil, map[string]bool{"__method_Par_elem": true}},
	} {
		refused := inferParamEscapes(prog, info, gate.pairForm, gate.trmcFns)
		if e := refused["__method_Par_elem"]; len(e) == 0 || !e[0] {
			t.Errorf("a %s function's projection return kept the credit; it must not — "+
				"the return is rewritten before the inc", gate.name)
		}
	}
}
