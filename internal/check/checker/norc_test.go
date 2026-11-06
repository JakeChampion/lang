package checker

import (
	"strings"
	"testing"
)

func TestNoRCAcceptsScalarCode(t *testing.T) {
	src := `const UART: usize = 4096;
@norc
function put(b: i32): void {
  unsafe {
    while ((__mmio_load32(UART + 24) & 32) != 0) {
    }
    __mmio_store8(UART, b);
  }
}
@norc
function sum(n: i32): i32 {
  let t = 0;
  let i = 0;
  while (i < n) {
    t += i;
    i += 1;
  }
  put(t);
  return t as i32;
}
function main(): i32 { return sum(5) - 10; }
`
	if errs := checkErrors(t, src); len(errs) != 0 {
		t.Fatalf("scalar @norc code refused: %v", errs)
	}
}

func TestNoRCRefusesEachBreach(t *testing.T) {
	src := `function helper(x: i32): i32 {
  return x + 1;
}
@norc
function f(s: string, n: i32): i32 {
  let xs: i32[] = [1, 2];
  let t = "hi";
  let u = helper(n);
  for i in 0..n {
    u += i;
  }
  return u + xs.len();
}
function main(): i32 { return f("a", 1); }
`
	want := []struct{ pos, msg string }{
		{"5:12", `takes "s" of type string, which is not a scalar`},
		{"6:3", `declares "xs" of type i32[], which is not a scalar`},
		{"6:19", "may not use an array literal"},
		{"7:11", "may not use a string"},
		{"8:17", "may call only `@norc` functions and the raw memory intrinsics, not \"helper\""},
		{"9:3", "may not use a `for` loop, which iterates a value; use `while`"},
		{"12:20", "may not call a method or a function value"},
	}
	errs := checkErrors(t, src)
	got := map[string]string{}
	for _, e := range errs {
		if e.ErrCode == "E102" {
			got[e.Pos.String()] = e.Msg
		}
	}
	for _, w := range want {
		msg, ok := got[w.pos]
		if !ok || !strings.HasSuffix(msg, w.msg) || !strings.HasPrefix(msg, "`@norc` function \"f\" ") {
			t.Errorf("at %s: got %q, want E102 ending %q (all: %v)", w.pos, msg, w.msg, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d E102 diagnostics, want %d: %v", len(got), len(want), got)
	}
}

func TestNoRCRefusesAMethod(t *testing.T) {
	src := `struct Box { v: i32 }
@norc
function (b: Box) get(): i32 {
  return 1;
}
function main(): i32 { return 0; }
`
	for _, e := range checkErrors(t, src) {
		if e.ErrCode == "E102" && strings.HasSuffix(e.Msg, "must be a plain function, not a method or a generic function") {
			return
		}
	}
	t.Error("a @norc method was accepted")
}
