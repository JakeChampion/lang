package e2e

import (
	"strings"
	"testing"
)

const knownVariantDropSrc = `enum Expr { Val(i32), Neg(Expr) }

@noinline
function vals(own a: Expr, own b: Expr): i32 {
  match (a) {
    Val(x) => {
      match (b) {
        Val(y) => {
          return x + y;
        },
        _ => {
          return x;
        }
      }
    },
    _ => {
      return 0;
    }
  }
}

@noinline
function val(n: i32): Expr {
  return Val(n);
}

@noinline
function neg(n: i32): Expr {
  return Neg(Val(n));
}

function main(): i32 {
  let bad: i32 = 0;
  if (vals(val(3), val(4)) != 7 || vals(val(3), neg(1)) != 3 || vals(neg(2), val(5)) != 0) {
    bad = bad + 1;
  }
  let a: Expr = val(8);
  let b: Expr = neg(9);
  let held: Expr[] = [a, b];
  if (vals(a, b) != 8 || held.len() != 2) {
    bad = bad + 2;
  }
  let c: Expr = val(1);
  let d: Expr = val(2);
  let held2: Expr[] = [c, d];
  if (vals(c, d) != 3 || held2.len() != 2) {
    bad = bad + 4;
  }
  return bad;
}`

func checkKnownVariantDrop(t *testing.T, run censusRun) {
	t.Helper()
	stdout, stderr, exit := run(t, knownVariantDropSrc)
	if exit != 0 || stdout != "" && stdout != "0\n" {
		t.Fatalf("exit %d, stdout %q, want 0 (bits 1..4 name the call that came back wrong); stderr: %s", exit, stdout, stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("sanitizer report:\n%s", stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Errorf("census: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestInterpKnownVariantDropValues(t *testing.T) {
	if got := runInterpExit(t, knownVariantDropSrc); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestX86_64KnownVariantDropSanitized(t *testing.T) {
	checkKnownVariantDrop(t, runSanitizeX86_64)
}

func TestArm64KnownVariantDropSanitized(t *testing.T) {
	checkKnownVariantDrop(t, runSanitizeArm64)
}

func TestWASMKnownVariantDropSanitized(t *testing.T) {
	checkKnownVariantDrop(t, func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
