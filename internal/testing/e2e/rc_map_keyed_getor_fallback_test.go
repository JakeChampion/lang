package e2e

import (
	"testing"
)

func keyedGetOrFallbackSrc(n string) string {
	return `import "core/map";
import "core/cmp" as cmp;

@derive(cmp.Eq, cmp.Hash)
struct Point { x: i32, y: i32 }

function main(): i32 {
    let m: Map[Point, i32[]] = map_new(8);
    m = m.insert(Point { x: 1, y: 2 }, [7, 8, 9]);
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < ` + n + `) {
        // A MISS every round: the key is a fresh struct temp and the
        // fallback a fresh counted-read array.
        let got: i32[] = m.get_or(Point { x: i + 100, y: 0 }, [1, 2, 3, 4, 5, 6]);
        t = t + got.len();
        i = i + 1;
    }
    if (t != ` + n + ` * 6) { return 99; }
    return 0;
}`
}

const keyedGetOrFallbackUnderflowSrc = `import "core/map";
import "core/cmp" as cmp;

@derive(cmp.Eq, cmp.Hash)
struct Point { x: i32, y: i32 }
@derive(cmp.Eq, cmp.Hash)
enum Tag { A(i32), B }

function main(): i32 {
    let acc: i32 = 0;
    let m: Map[Point, i32[]] = map_new(8);
    m = m.insert(Point { x: 1, y: 2 }, [7, 8, 9]);
    let sm: Map[Tag, string[]] = map_new(8);
    sm = sm.insert(Tag.A(1), ["a-wide-payload-past-any-inline-threshold"]);
    let i: i32 = 0;
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
	if got := runWasm(t, keyedGetOrFallbackUnderflowSrc); got != 0 {
		t.Errorf("keyed get_or: code=%d (99=wrong value, >0=over-release)", got)
	}
}

func keyedGetOrLeak(t *testing.T, stderr string) (int64, int64) {
	t.Helper()
	allocs, _, live := parseLeakCheckLine(t, stderr)
	return allocs, live
}
