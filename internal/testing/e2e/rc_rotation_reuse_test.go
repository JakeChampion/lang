package e2e

import (
	"fmt"
	"strings"
	"testing"
)

func rotationReuseSrc(rounds int, shared bool) string {
	keep := ""
	check := "false"
	if shared {
		keep = "let old: E = t;"
		check = "sum(old) != 18"
	}
	return fmt.Sprintf(`enum E { Leaf(i32), Two(E, E) }

@noinline
function rot(t: E): E {
    match (t) {
        Two(Two(a, b), c) => { return Two(a, Two(b, c)); },
        _ => { return t; }
    }
}

@noinline
function unrot(t: E): E {
    match (t) {
        Two(a, Two(b, c)) => { return Two(Two(a, b), c); },
        _ => { return t; }
    }
}

function sum(t: E): i32 {
    match (t) {
        Leaf(n) => { return n; },
        Two(a, b) => { return sum(a) * 3 + sum(b); }
    }
}

function main(): i32 {
    let t: E = Two(Two(Leaf(1), Leaf(2)), Leaf(3));
    let i: i32 = 0;
    while (i < %d) {
        %s
        t = rot(t);
        if (sum(t) != 12) { return 1; }
        t = unrot(t);
        if (%s || sum(t) != 18) { return 1; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`, rounds, keep, check)
}

func checkRotationReuse(t *testing.T, target string, run censusRun) {
	t.Helper()
	census := func(rounds int, shared bool) int64 {
		stdout, stderr, exit := run(t, rotationReuseSrc(rounds, shared))
		if exit != 0 || strings.TrimSpace(stdout) != "" && strings.TrimSpace(stdout) != "0" {
			t.Fatalf("%s rounds=%d shared=%v: exit %d, stdout %q, want 0; stderr: %s", target, rounds, shared, exit, stdout, stderr)
		}
		a, f, live := parseLeakCheckLine(t, stderr)
		if a != f || live != 0 {
			t.Fatalf("%s rounds=%d shared=%v: allocs=%d frees=%d live_bytes=%d, want balanced", target, rounds, shared, a, f, live)
		}
		t.Logf("%s rounds=%d shared=%v: allocs=%d", target, rounds, shared, a)
		return a
	}
	if lo, hi := census(100, false), census(400, false); lo != hi {
		t.Errorf("%s unique: %d allocations at 100 rounds and %d at 400, want the same: a rotation allocated", target, lo, hi)
	}
	if lo, hi := census(100, true), census(400, true); hi-lo != 300*2 {
		t.Errorf("%s shared: %d allocations at 100 rounds and %d at 400, want %d more: a retained tree must be copied", target, lo, hi, 300*2)
	}
}

func TestInterpRotationReuseValues(t *testing.T) {
	for _, shared := range []bool{false, true} {
		if got := runInterpExit(t, rotationReuseSrc(50, shared)); got != 0 {
			t.Errorf("shared=%v: got %d, want 0", shared, got)
		}
	}
}

func TestX86_64RotationReuse(t *testing.T) {
	checkRotationReuse(t, "x86-64", runLeakCheckX86_64)
}

func TestArm64RotationReuse(t *testing.T) {
	checkRotationReuse(t, "arm64", runLeakCheckArm64)
}

func TestWASMRotationReuse(t *testing.T) {
	checkRotationReuse(t, "wasm", func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
