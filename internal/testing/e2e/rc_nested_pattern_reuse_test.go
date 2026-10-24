package e2e

import (
	"fmt"
	"testing"
)

func nestedPatternReuseSrc(rounds int, shared bool) string {
	keep := "let old: i32 = value(t);"
	check := "old != i - 1"
	if shared {
		keep = "let old: Tree = t;"
		check = "value(old) != i - 1 || size(old) != 3"
	}
	return fmt.Sprintf(`enum Color { Red, Black }
enum Tree { Leaf, Node(Color, Tree, i32, Tree) }

@noinline
function update(t: Tree, n: i32): Tree {
    match (t) {
        Node(Red(), l, _, r) => { return Node(Red, l, n, r); },
        Node(Black(), l, _, r) => { return Node(Black, l, n, r); },
        Leaf => { return Node(Red, Leaf, n, Leaf); }
    }
}

@noinline
function paint(t: Tree, black: boolean): Tree {
    match (t) {
        Node(_, l, n, r) => { if (black) { return Node(Black, l, n, r); } return Node(Red, l, n, r); },
        Leaf => { return t; }
    }
}

function value(t: Tree): i32 {
    match (t) {
        Node(_, _, n, _) => { return n; },
        Leaf => { return 0 - 1; }
    }
}

function black(t: Tree): boolean {
    match (t) {
        Node(Black(), _, _, _) => { return true; },
        _ => { return false; }
    }
}

function size(t: Tree): i32 {
    match (t) {
        Node(_, l, _, r) => { return 1 + size(l) + size(r); },
        Leaf => { return 0; }
    }
}

function main(): i32 {
    let i: i32 = 0 - 1;
    let t: Tree = update(Leaf, i);
    t = Node(Black, update(Leaf, 7), value(t), update(Leaf, 8));
    i = 0;
    while (i < %d) {
        t = paint(t, i %% 3 == 0);
        let was: boolean = black(t);
        %s
        t = update(t, i);
        if (%s || value(t) != i || black(t) != was || size(t) != 3) { return 1; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`, rounds, keep, check)
}

type censusRun func(t *testing.T, src string) (string, string, int)

func checkNestedPatternReuse(t *testing.T, target string, run censusRun) {
	t.Helper()
	census := func(rounds int, shared bool) int64 {
		_, stderr, exit := run(t, nestedPatternReuseSrc(rounds, shared))
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
		t.Errorf("%s unique: %d allocations at 100 rounds and %d at 400, want the same: the nested arm does not reuse its node", target, lo, hi)
	}
	if lo, hi := census(100, true), census(400, true); hi-lo != 300 {
		t.Errorf("%s shared: %d allocations at 100 rounds and %d at 400, want 300 more: a retained snapshot must be copied", target, lo, hi)
	}
}

func TestInterpNestedPatternReuseValues(t *testing.T) {
	for _, shared := range []bool{false, true} {
		if got := runInterpExit(t, nestedPatternReuseSrc(50, shared)); got != 0 {
			t.Errorf("shared=%v: got %d, want 0", shared, got)
		}
	}
}

func TestX86_64NestedPatternReuse(t *testing.T) {
	checkNestedPatternReuse(t, "x86-64", runLeakCheckX86_64)
}

func TestArm64NestedPatternReuse(t *testing.T) {
	checkNestedPatternReuse(t, "arm64", runLeakCheckArm64)
}

func TestWASMNestedPatternReuse(t *testing.T) {
	checkNestedPatternReuse(t, "wasm", func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
