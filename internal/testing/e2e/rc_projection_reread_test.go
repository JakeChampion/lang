package e2e

import (
	"fmt"
	"testing"
)

func projectionRereadSrc(rounds int, shared bool) string {
	keep := "let old: i32 = leftmost(t);"
	check := "old != i"
	if shared {
		keep = "let old: Tree = t;"
		check = "leftmost(old) != i || depth(old) != 20"
	}
	return fmt.Sprintf(`enum Color { Red, Black }
enum Tree { Leaf, Node(Color, Tree, i32, Tree) }

function is_leaf(t: Tree): boolean {
    match (t) {
        Leaf => { return true; },
        _ => { return false; }
    }
}

@noinline
function bump(t: Tree): Tree {
    match (t) {
        Node(Red(), l, n, r) => {
            if (is_leaf(l)) { return Node(Red, l, n + 1, r); }
            return Node(Red, bump(l), n, r);
        },
        Node(Black(), l, n, r) => {
            if (is_leaf(l)) { return Node(Black, l, n + 1, r); }
            return Node(Black, bump(l), n, r);
        },
        Leaf => { return t; }
    }
}

function spine(n: i32): Tree {
    let t: Tree = Leaf;
    let i: i32 = 0;
    while (i < n) {
        if (i %% 2 == 0) { t = Node(Red, t, 0, Node(Black, Leaf, i, Leaf)); } else { t = Node(Black, t, 0, Leaf); }
        i = i + 1;
    }
    return t;
}

function leftmost(t: Tree): i32 {
    match (t) {
        Node(_, l, n, _) => { if (is_leaf(l)) { return n; } return leftmost(l); },
        Leaf => { return 0 - 1; }
    }
}

function depth(t: Tree): i32 {
    match (t) {
        Node(_, l, _, _) => { return 1 + depth(l); },
        Leaf => { return 0; }
    }
}

function main(): i32 {
    let t: Tree = spine(20);
    let i: i32 = 0;
    while (i < %d) {
        %s
        t = bump(t);
        if (%s || leftmost(t) != i + 1 || depth(t) != 20) { return 1; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`, rounds, keep, check)
}

func checkProjectionReread(t *testing.T, target string, run censusRun) {
	t.Helper()
	census := func(rounds int, shared bool) int64 {
		_, stderr, exit := run(t, projectionRereadSrc(rounds, shared))
		if exit != 0 {
			t.Fatalf("%s rounds=%d shared=%v: exit %d, want 0; stderr: %s", target, rounds, shared, exit, stderr)
		}
		a, f, live := parseLeakCheckLine(t, stderr)
		if a != f || live != 0 {
			t.Fatalf("%s rounds=%d shared=%v: allocs=%d frees=%d live_bytes=%d, want balanced", target, rounds, shared, a, f, live)
		}
		t.Logf("%s rounds=%d shared=%v: allocs=%d", target, rounds, shared, a)
		return a
	}
	if lo, hi := census(100, false), census(400, false); lo != hi {
		t.Errorf("%s unique: %d allocations at 100 rounds and %d at 400, want the same: a level found its node shared", target, lo, hi)
	}
	if lo, hi := census(100, true), census(400, true); hi-lo != 300*20 {
		t.Errorf("%s shared: %d allocations at 100 rounds and %d at 400, want %d more: a retained tree must be copied", target, lo, hi, 300*20)
	}
}

func TestInterpProjectionRereadValues(t *testing.T) {
	for _, shared := range []bool{false, true} {
		if got := runInterpExit(t, projectionRereadSrc(50, shared)); got != 0 {
			t.Errorf("shared=%v: got %d, want 0", shared, got)
		}
	}
}

func TestX86_64ProjectionReread(t *testing.T) {
	checkProjectionReread(t, "x86-64", runLeakCheckX86_64)
}

func TestArm64ProjectionReread(t *testing.T) {
	checkProjectionReread(t, "arm64", runLeakCheckArm64)
}

func TestWASMProjectionReread(t *testing.T) {
	checkProjectionReread(t, "wasm", func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
