package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// The struct/enum-key (keyKind-3) `get_or` path emitted both non-receiver
// arguments inline, with no stashOwnedArgTemp / emitArgTempDrops — so neither
// temp was ever ended. `get_or` only READS the key, and the counted-read value
// column retains the fallback on a miss, so both are dead at the call.
//
// The key half leaked a struct a round on its own. The fallback half arrived
// with the miss-retain (#7910 (a)), whose !keyKind3 arm ends the fallback temp
// and whose keyed sibling did not: 48 B a round on top, on a column the
// composite-key leak matrix never covered (its rows all use i32 / string value
// columns, where neither temp is counted).
//
// Reported by a review bot on #8215, measured at 64 B a round on x86-64, and
// bounded here rather than pinned to a number: the residue is the map and its
// one entry, which this shape does not reclaim either way.

func keyedGetOrFallbackSrc(n string) string {
	return `import "core/map";
import "core/cmp" as cmp;

@derive(cmp.Eq, cmp.Hash)
struct Point { x: i32, y: i32 }

function main(): i32 {
    var m: Map[Point, i32[]] = map_new(8);
    m = m.insert(Point { x: 1, y: 2 }, [7, 8, 9]);
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < ` + n + `) {
        // A MISS every round: the key is a fresh struct temp and the
        // fallback a fresh counted-read array.
        var got: i32[] = m.get_or(Point { x: i + 100, y: 0 }, [1, 2, 3, 4, 5, 6]);
        t = t + got.len();
        i = i + 1;
    }
    if (t != ` + n + ` * 6) { return 99; }
    return 0;
}`
}

// The hit path must still hand back the map's own value, and a string[] column
// must reclaim the fallback's strings rather than the map's. 0 iff every read
// saw the right thing AND nothing dec'd past zero.
const keyedGetOrFallbackUnderflowSrc = `import "core/map";
import "core/cmp" as cmp;

@derive(cmp.Eq, cmp.Hash)
struct Point { x: i32, y: i32 }
@derive(cmp.Eq, cmp.Hash)
enum Tag { A(i32), B }

function main(): i32 {
    var acc: i32 = 0;
    var m: Map[Point, i32[]] = map_new(8);
    m = m.insert(Point { x: 1, y: 2 }, [7, 8, 9]);
    var sm: Map[Tag, string[]] = map_new(8);
    sm = sm.insert(Tag.A(1), ["a-wide-payload-past-any-inline-threshold"]);
    var i: i32 = 0;
    while (i < 200) {
        // hit: the map's own value, which must outlive the call
        acc = acc + (m.get_or(Point { x: 1, y: 2 }, [0]).len() - 3);
        // miss: the fallback, whose temp the call ends
        acc = acc + (m.get_or(Point { x: i + 100, y: 0 }, [1, 2, 3, 4, 5, 6]).len() - 6);
        // enum key over a string[] column, both outcomes
        acc = acc + (sm.get_or(Tag.A(1), []).len() - 1);
        acc = acc + (sm.get_or(Tag.B, ["x-wide-payload-past-any-inline-threshold", "y-wide-payload-past-any-inline-threshold"]).len() - 2);
        i = i + 1;
    }
    // the map's entries must still be intact after every round
    acc = acc + (m.get_or(Point { x: 1, y: 2 }, [0])[2] - 9);
    acc = acc + (sm.get_or(Tag.A(1), [])[0].len() - 40);
    if (acc != 0) { return 99; }
    return __rc_underflow_count();
}`

func TestX86_64MapKeyedGetOrFallbackReclaim(t *testing.T) {
	_, stderrSmall, code := runLeakCheckX86_64(t, keyedGetOrFallbackSrc("50"))
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	_, liveSmall := keyedGetOrLeak(t, stderrSmall)
	_, stderrLarge, code := runLeakCheckX86_64(t, keyedGetOrFallbackSrc("2000"))
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocsLarge, liveLarge := keyedGetOrLeak(t, stderrLarge)
	if allocsLarge == 0 {
		t.Fatalf("expected allocations (struct keys and fallback arrays), got 0")
	}
	if liveSmall != liveLarge {
		t.Errorf("keyed get_or temps leak per round: N=50 live=%d, N=2000 live=%d, want equal", liveSmall, liveLarge)
	}
	if _, code := compileAndRunX86_64FreeOn(t, keyedGetOrFallbackUnderflowSrc); code != 0 {
		t.Errorf("keyed get_or: code=%d (99=wrong value, >0=over-release)", code)
	}
}

func TestArm64MapKeyedGetOrFallbackReclaim(t *testing.T) {
	_, stderrSmall, code := runLeakCheckArm64(t, keyedGetOrFallbackSrc("50"))
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	_, liveSmall := keyedGetOrLeak(t, stderrSmall)
	_, stderrLarge, code := runLeakCheckArm64(t, keyedGetOrFallbackSrc("2000"))
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocsLarge, liveLarge := keyedGetOrLeak(t, stderrLarge)
	if allocsLarge == 0 {
		t.Fatalf("expected allocations (struct keys and fallback arrays), got 0")
	}
	if liveSmall != liveLarge {
		t.Errorf("keyed get_or temps leak per round: N=50 live=%d, N=2000 live=%d, want equal", liveSmall, liveLarge)
	}
	if _, code := compileAndRunArm64FreeOn(t, keyedGetOrFallbackUnderflowSrc); code != 0 {
		t.Errorf("keyed get_or: code=%d (99=wrong value, >0=over-release)", code)
	}
}

func TestWASMMapKeyedGetOrFallbackReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, keyedGetOrFallbackUnderflowSrc); got != 0 {
		t.Errorf("keyed get_or: code=%d (99=wrong value, >0=over-release)", got)
	}
}

func keyedGetOrLeak(t *testing.T, stderr string) (int64, int64) {
	t.Helper()
	allocs, _, live := parseLeakCheckLine(t, stderr)
	return allocs, live
}
