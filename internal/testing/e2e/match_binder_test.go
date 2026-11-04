package e2e

import (
	"testing"
)

// A bare name in arm position of a match on a non-enum value binds the
// scrutinee (#14099). A guarded `_` or binding arm may precede other arms.
var matchBindingArmCases = []struct {
	name string
	src  string
	want int
}{
	{"expr-guarded-mid", `function f(k: i32): i32 {
  return match (k) { 0 => 10, n when n > 100 => n, _ => 14 };
}
function main(): i32 { return f(0) + f(150) + f(5); }`, 174},
	{"stmt-guarded-mid", `function f(k: i32): i32 {
  match (k) {
    0 => { return 10; },
    n when n > 100 => { return n; },
    _ => { return 14; }
  }
}
function main(): i32 { return f(0) + f(150) + f(5); }`, 174},
	{"binder-first-arm", `function f(k: i32): i32 {
  return match (k) { n when n > 100 => n, 0 => 10, _ => 14 };
}
function main(): i32 { return f(0) + f(150) + f(5); }`, 174},
	{"no-literal-arm", `function f(k: i32): i32 {
  return match (k) { n when n > 0 => n * 2, _ => 7 };
}
function g(k: i32): i32 {
  match (k) {
    n when n > 0 => { return n * 3; },
    m => { return m + 20; }
  }
}
function main(): i32 { return f(3) + f(-3) + g(2) + g(-1); }`, 38},
	{"unguarded-binder-last", `function f(k: i32): i32 {
  return match (k) { 0 => 1, n => n + 1 };
}
function main(): i32 { return f(0) + f(41); }`, 43},
	{"string-scrutinee", `function f(s: string): i32 {
  return match (s) { "a" => 1, other => other.len() };
}
function main(): i32 { return f("a") + f("xyz"); }`, 4},
	{"call-scrutinee", `function twice(k: i32): i32 { return k * 2; }
function f(k: i32): i32 {
  return match (twice(k)) { 0 => 10, n when n > 100 => n, _ => 14 };
}
function main(): i32 { return f(0) + f(75) + f(5); }`, 174},
	{"guarded-wildcard-mid-literal", `function f(k: i32): i32 {
  return match (k) { 0 => 10, _ when k > 100 => k, _ => 14 };
}
function main(): i32 { return f(0) + f(150) + f(5); }`, 174},
	{"guarded-wildcard-mid-enum", `function f(o: Option[i32], c: boolean): i32 {
  match (o) {
    _ when c => { return 1; },
    Some(x) => { return x; },
    None => { return 50; }
  }
}
function main(): i32 { return f(Some(7), true) + f(Some(7), false) + f(None, false); }`, 58},
}

func TestX86_64MatchBindingArm(t *testing.T) {
	for _, c := range matchBindingArmCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Run("interp", func(t *testing.T) {
				if code := runInterpByte(t, c.src); code != c.want {
					t.Errorf("interp: got %d, want %d", code, c.want)
				}
			})
			if _, code := compileAndRunX86_64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

func TestArm64MatchBindingArm(t *testing.T) {
	for _, c := range matchBindingArmCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

func TestWASMMatchBindingArm(t *testing.T) {
	for _, c := range matchBindingArmCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}
