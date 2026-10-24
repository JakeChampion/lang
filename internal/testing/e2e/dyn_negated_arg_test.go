package e2e

import (
	"strings"
	"testing"
)

const dynNegatedArgSrc = `trait Tr {
  function m(self: Self, x: i32): i32;
}

impl Tr for i32 {
  function m(self: Self, x: i32): i32 {
    return self * 10 + x;
  }
}

impl Tr for i64 {
  function m(self: Self, x: i32): i32 {
    return (self as i32) - x;
  }
}

function call(d: dyn Tr, x: i32): i32 {
  return d.m(x);
}

function main(): i32 {
  let a: i32 = call(-5, 3);
  let b: i32 = call(-7i64, 2);
  let c: i32 = call(-(4 + 1), 1);
  if (a != 0 - 47 || b != 0 - 9 || c != 0 - 49) {
    return 1;
  }
  return 0;
}
`

func TestInterpDynNegatedArg(t *testing.T) {
	if got := runInterpExit(t, dynNegatedArgSrc); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func checkDynNegatedArg(t *testing.T, run censusRun) {
	t.Helper()
	stdout, stderr, exit := run(t, dynNegatedArgSrc)
	if exit != 0 || stdout != "" && stdout != "0\n" {
		t.Fatalf("exit %d, stdout %q, want 0; stderr: %s", exit, stdout, stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("sanitizer report:\n%s", stderr)
	}
}

func TestX86_64DynNegatedArgSanitized(t *testing.T) {
	checkDynNegatedArg(t, runSanitizeX86_64)
}

func TestArm64DynNegatedArgSanitized(t *testing.T) {
	checkDynNegatedArg(t, runSanitizeArm64)
}

func TestWASMDynNegatedArgSanitized(t *testing.T) {
	checkDynNegatedArg(t, func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
