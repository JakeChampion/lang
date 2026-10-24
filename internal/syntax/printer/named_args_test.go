package printer

import (
	"strings"
	"testing"
)

func TestFormatKeepsNamedArguments(t *testing.T) {
	got := formatSrc(t, `function mk(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return mk(b = 1, a = 9); }
`)
	if !strings.Contains(got, "mk(b = 1, a = 9)") {
		t.Errorf("named arguments lost their names or their order:\n%s", got)
	}
}

func TestFormatKeepsNamedArgumentsThroughPipe(t *testing.T) {
	got := formatSrc(t, `function mk(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return 9 |> mk(b = 1); }
`)
	if !strings.Contains(got, "mk(b = 1)") {
		t.Errorf("named argument lost through a pipe call:\n%s", got)
	}
}

func TestFormatKeepsNamedArgumentOnPipeHole(t *testing.T) {
	got := formatSrc(t, `function diff(a: i32 = 0, b: i32 = 0): i32 { return a - b; }
function main(): i32 { return 9 |> diff(b = _); }
`)
	if !strings.Contains(got, "diff(b = _)") {
		t.Errorf("the pipe hole lost its argument name:\n%s", got)
	}
}

func TestFormatKeepsDefaultParameterValues(t *testing.T) {
	got := formatSrc(t, `function diff(a: i32 = 0, b: i32 = 0): i32 { return a - b; }
function main(): i32 { return diff(a = 4); }
`)
	if !strings.Contains(got, "a: i32 = 0") || !strings.Contains(got, "b: i32 = 0") {
		t.Errorf("default parameter values were dropped from the signature:\n%s", got)
	}
}

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
