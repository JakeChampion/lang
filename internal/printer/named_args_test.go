package printer

import (
	"strings"
	"testing"
)

// A named argument keeps its `name = ` prefix through formatting. Args holds
// them in SOURCE order with the names alongside in ArgNames, so dropping the
// names rewrote `mk(b = 1, a = 9)` as `mk(1, 9)` — binding each value to the
// other parameter. `-fmt` silently changed what the program computed: the
// original returned 8, the formatted one -8.
func TestFormatKeepsNamedArguments(t *testing.T) {
	got := formatSrc(t, `function mk(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return mk(b = 1, a = 9); }
`)
	if !strings.Contains(got, "mk(b = 1, a = 9)") {
		t.Errorf("named arguments lost their names or their order:\n%s", got)
	}
}

// The same on a pipe call, whose trailing arguments run through their own
// emission path.
func TestFormatKeepsNamedArgumentsThroughPipe(t *testing.T) {
	got := formatSrc(t, `function mk(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return 9 |> mk(b = 1); }
`)
	if !strings.Contains(got, "mk(b = 1)") {
		t.Errorf("named argument lost through a pipe call:\n%s", got)
	}
}

// The pipe's `_` hole can itself be a named argument's value. The name lives in
// ArgNames at the hole's index, and the hole branch emitted a bare `_` — so
// `9 |> f(b = _)` re-parsed as `9 |> f(_)`, binding the piped value to the
// FIRST parameter instead. With `diff(a, b) = a - b` the original computes
// 0 - 9 and the formatted one 9 - 0.
func TestFormatKeepsNamedArgumentOnPipeHole(t *testing.T) {
	got := formatSrc(t, `function diff(a: i32 = 0, b: i32 = 0): i32 { return a - b; }
function main(): i32 { return 9 |> diff(b = _); }
`)
	if !strings.Contains(got, "diff(b = _)") {
		t.Errorf("the pipe hole lost its argument name:\n%s", got)
	}
}

// A default parameter value is part of the signature. Dropping it made the
// formatted function require an argument its callers omit, so `-fmt` turned a
// compiling program into one that does not compile.
func TestFormatKeepsDefaultParameterValues(t *testing.T) {
	got := formatSrc(t, `function diff(a: i32 = 0, b: i32 = 0): i32 { return a - b; }
function main(): i32 { return diff(a = 4); }
`)
	if !strings.Contains(got, "a: i32 = 0") || !strings.Contains(got, "b: i32 = 0") {
		t.Errorf("default parameter values were dropped from the signature:\n%s", got)
	}
}

// Nested function declarations print through their own parameter path.
func TestFormatKeepsDefaultsOnNestedFunction(t *testing.T) {
	got := formatSrc(t, `function main(): i32 {
  function inner(n: i32 = 3): i32 { return n; }
  return inner();
}
`)
	if !strings.Contains(got, "n: i32 = 3") {
		t.Errorf("a nested function lost its default parameter value:\n%s", got)
	}
}
