package parser

import (
	"strings"
	"testing"
)

// A negative number is a literal, and a literal is a pattern. The sign is a
// separate token from the digits, so the single-token "does a literal start
// here" test declined `-1` and the arm was rejected — by a message that
// listed "literal" among what it accepted.
//
// Every arm position that takes a literal takes a negative one.
func TestNegativeLiteralPatterns(t *testing.T) {
	sources := []struct {
		name string
		src  string
	}{
		{"plain arm", `function f(v: i32): i32 {
    match (v) { -1 => { return 1; }, 0 => { return 2; }, _ => { return 3; } }
}`},
		{"or-pattern alternative", `function f(v: i32): i32 {
    match (v) { 1 | -3 => { return 1; }, _ => { return 2; } }
}`},
		{"range with a negative low", `function f(v: i32): i32 {
    match (v) { -10..0 => { return 1; }, _ => { return 2; } }
}`},
		{"range with both bounds negative", `function f(v: i32): i32 {
    match (v) { -100..-10 => { return 1; }, _ => { return 2; } }
}`},
		{"inclusive range", `function f(v: i32): i32 {
    match (v) { -5..=-1 => { return 1; }, _ => { return 2; } }
}`},
		{"tuple element", `function f(t: (i32, i32)): i32 {
    match (t) { (-1, y) => { return 1; }, (x, -2) => { return 2; }, _ => { return 3; } }
}`},
		{"float literal", `function f(v: f64): i32 {
    match (v) { -1.5 => { return 1; }, _ => { return 2; } }
}`},
		{"the smallest i32", `function f(v: i32): i32 {
    match (v) { -2147483648 => { return 1; }, _ => { return 2; } }
}`},
	}
	for _, c := range sources {
		if _, err := Parse(c.src); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// `-` in arm position only introduces a number. An identifier there is a
// variant name, not a value to negate, and must stay rejected rather than
// being read as a negation of something.
func TestNegativeArmRequiresANumber(t *testing.T) {
	_, err := Parse(`function f(v: i32): i32 {
    match (v) { -x => { return 1; }, _ => { return 2; } }
}`)
	if err == nil {
		t.Fatal("`-x` in arm position was accepted")
	}
	if !strings.Contains(err.Error(), "match arm") {
		t.Errorf("rejection does not mention the match arm: %v", err)
	}
}
