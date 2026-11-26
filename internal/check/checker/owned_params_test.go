package checker

import "testing"

func wantOK(t *testing.T, name, src string) {
	t.Helper()
	if err := checkSource(t, src); err != nil {
		t.Errorf("%s: expected no error, got: %v", name, err)
	}
}

const ownPrelude = `enum Lst { Cons(i32), Nil }
function sink(own xs: i32[]): i32 { return xs[0]; }
function peek(xs: i32[]): i32 { return xs[0]; }
function lsink(l: Lst): i32 { return 0; }
`

func wantE051(t *testing.T, name, src string) {
	t.Helper()
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("%s: expected an E051 ownership error, got none", name)
	}
	if !hasCode(err, "E051") {
		t.Errorf("%s: expected an E051 error, got: %v", name, err)
	}
}

func TestParamNamedOwnStillWorks(t *testing.T) {
	wantOK(t, "param-named-own", `
function f(own: i32): i32 { return own + 1; }
function main(): i32 { return f(41); }`)
}

func TestOwnModifierParses(t *testing.T) {
	wantOK(t, "own-modifier-parses", ownPrelude+`
function f(own xs: i32[]): i32 { return xs[0]; }
function main(): i32 { return f([1, 2]); }`)
}
