package e2e

import (
	"strings"
	"testing"
)

const genericCallInGuardSrc = `trait Tr {
  function m(self: Self, x: i32): i32;
}

impl Tr for i32 {
  function m(self: Self, x: i32): i32 {
    return self * 10 + x;
  }
}

enum Shape { Dot(i32), Line(i32, i32), Blank }

struct Holder[T] { v: T }

function big(h: Holder[i32]): boolean {
  return h.v > 3;
}

function g[T: Tr](a: T, b: T, x: i32): i32 {
  return a.m(x) + b.m(x);
}

function pick[T](a: T, b: T, first: boolean): T {
  if (first) {
    return a;
  }
  return b;
}

function classify(s: Shape): i32 {
  match (s) {
    Dot(v) when g(v, 1, 2) > 50 => {
      return 1;
    },
    Line(a, b) when pick(a, b, a > b) > 3 => {
      return 2;
    },
    Dot(_) => {
      return 3;
    },
    _ => {
      return 4;
    }
  }
}

function main(): i32 {
  let o: Option[i32] = Some(4);
  let bad: i32 = 0;
  match (o) {
    Some(v) when g(v, 1, 2) > 50 => {
      bad = bad + 0;
    },
    _ => {
      bad = bad + 1;
    }
  }
  if (classify(Dot(4)) != 1 || classify(Dot(1)) != 3 || classify(Line(1, 9)) != 2 || classify(Line(1, 2)) != 4 || classify(Blank) != 4) {
    bad = bad + 2;
  }
  match (o) {
    Some(x) when big(Holder { v: x }) => {
      bad = bad + 0;
    },
    _ => {
      bad = bad + 4;
    }
  }
  return bad;
}
`

func TestInterpGenericCallInGuard(t *testing.T) {
	if got := runInterpExit(t, genericCallInGuardSrc); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func checkGenericCallInGuard(t *testing.T, run censusRun) {
	t.Helper()
	stdout, stderr, exit := run(t, genericCallInGuardSrc)
	if exit != 0 || stdout != "" && stdout != "0\n" {
		t.Fatalf("exit %d, stdout %q, want 0 (bit 1: an Option guard, 2: a user enum's, 4: a generic struct literal); stderr: %s", exit, stdout, stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("sanitizer report:\n%s", stderr)
	}
}

func TestX86_64GenericCallInGuardSanitized(t *testing.T) {
	checkGenericCallInGuard(t, runSanitizeX86_64)
}

func TestArm64GenericCallInGuardSanitized(t *testing.T) {
	checkGenericCallInGuard(t, runSanitizeArm64)
}

func TestWASMGenericCallInGuardSanitized(t *testing.T) {
	checkGenericCallInGuard(t, func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
