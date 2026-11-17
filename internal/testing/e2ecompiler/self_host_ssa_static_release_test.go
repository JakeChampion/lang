package e2ecompiler

import (
	"strings"
	"testing"
)

// A Nil that a match finds is a static box, so its drop is no code. inc is a
// cons-tail loop: its Nil drop is on the edges of two variant tests. sum drops
// its Nil where the match leaves for the arm.
const staticReleaseSrc = `enum List { Nil, Cons(i64, List) }

@noinline
function make(n: i64): List {
  if (n == 0i64) {
    return Nil;
  }
  return Cons(n, make(n - 1i64));
}

@noinline
function inc(own xs: List): List {
  return match (xs) { Cons(x, rest) => Cons(x + 1i64, inc(rest)), Nil => Nil };
}

@noinline
function sum(own xs: List): i64 {
  return match (xs) { Cons(x, rest) => x + sum(rest), Nil => 0i64 };
}

function main(): i32 {
  let kept: List = make(100i64);
  let bad: i32 = 0;
  if (sum(inc(kept)) != 5150i64) {
    bad = bad + 1;
  }
  if (sum(kept) != 5050i64) {
    bad = bad + 2;
  }
  if (sum(inc(make(10i64))) != 65i64) {
    bad = bad + 4;
  }
  return bad;
}`

func staticReleaseShape(t *testing.T, asm string) {
	t.Helper()
	if body := fnText(t, asm, "sum"); strings.Contains(body, "__sem_release_List") {
		t.Errorf("sum releases its Nil, a static box:\n%s", body)
	}
}

func TestSelfHostSSAStaticReleaseX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, staticReleaseSrc)
	staticReleaseShape(t, asm)
	if got := run(t, "static", asm); got != 0 {
		t.Errorf("exit = %d, want 0 (bit 1: shared list, 2: the list after the walk, 4: fresh list)", got)
	}
	cli := newStrictCLI(t)
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", staticReleaseSrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
	quarantine := buildBin(t, cli.gcc, t.TempDir(), "quarantine", cli.emit(t, "x86-64-linux", staticReleaseSrc, "FERN_RC_FREE_DEBUG=1"))
	if stderr, exit := runWithStdin(t, cli.runner, quarantine, nil); exit != 0 {
		t.Fatalf("quarantine run: exit %d, want 0\n%s", exit, stderr)
	}
}

func TestSelfHostSSAStaticReleaseArm64(t *testing.T) {
	emit, run := arm64ShapeHarness(t)
	asm := emit(t, staticReleaseSrc)
	staticReleaseShape(t, asm)
	if got := run(t, "static", asm); got != 0 {
		t.Errorf("exit = %d, want 0 (bit 1: shared list, 2: the list after the walk, 4: fresh list)", got)
	}
}

func TestSelfHostSSAStaticReleaseWasm(t *testing.T) {
	cli := newStrictCLI(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", staticReleaseSrc)); code != 0 {
		t.Fatalf("wasm: exit %d, stdout %q; want 0", code, out)
	}
}
