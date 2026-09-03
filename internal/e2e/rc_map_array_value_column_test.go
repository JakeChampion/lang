package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #7910 (a) — a map whose VALUES are `string[]`.
//
// mapValHasDrop had no arm for a string-element array value, so the column
// fell to __map_drop_values' kind-2 release: one flat __fern_arr_dec per value,
// which frees each value's buffer and strands every string in it — two heap
// strings per insert, unbounded. The column now routes through
// __drop_map_via___drop_strarr, whose per-value drop is __fern_drop_arr_str
// (element walk at rc==1, a dec otherwise), the same release a `string[]`
// local, field or payload already gets.
//
// The strings are built by a call that branches on the loop variable, so
// nothing folds and nothing packs inline. FERN_LEAKCHECK is the instrument on
// the natives; wasm has no leak counter, so it rides the __heap_bump_bytes()
// high-water probe, flat under reclaim and linear under a leak.

const mapArrayValueColumnSrc = `import "core/map";
function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var m: Map[i32, string[]] = Map {};
        m = m.insert(i, [w(i), w(i + 1)]);
        acc = acc + m.get_or(i, []).len();
        i = i + 1;
    }
    if (acc != 200) { return 1; }
    return 0;
}`

// The same column read through a BOUND get_or result. get_or on a counted-read
// value column hands back a reference the caller owns on both outcomes (the
// runtime retains a hit's value and a miss's fallback alike), which is what
// lets the binding reclaim it and the temp form above end its fallback.
const mapArrayValueColumnBoundSrc = `import "core/map";
function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var m: Map[i32, string[]] = Map {};
        m = m.insert(i, [w(i), w(i + 1)]);
        var v: string[] = m.get_or(i, []);
        var d: string[] = m.get_or(i + 1, []);
        acc = acc + v.len() + d.len();
        i = i + 1;
    }
    if (acc != 200) { return 1; }
    return 0;
}`

func mapArrayValueColumnBumpSrc(n string) string {
	return `import "core/map";
function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < ` + n + `) {
        var m: Map[i32, string[]] = Map {};
        m = m.insert(i, [w(i), w(i + 1)]);
        acc = acc + m.get_or(i, []).len();
        i = i + 1;
    }
    if (acc < 0) { return acc; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Value correctness + zero over-release: a value read back out of the column
// after a second insert must still hold its strings, and the map's drop must
// release each value exactly once — the direction leakcheck cannot see.
const mapArrayValueColumnUnderflowSrc = `import "core/map";
function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var m: Map[i32, string[]] = Map {};
        m = m.insert(i, [w(i), w(i + 1)]);
        m = m.insert(i + 1, [w(i + 2)]);
        var got: string[] = m.get_or(i, []);
        acc = acc + got.len() + got[0].len();
        match (m.get(i + 1)) { Some(v) => { acc = acc + v.len(); }, None => { acc = acc + 100; } }
        i = i + 1;
    }
    // Per round: got.len() (2), got[0] = w(i) (45 chars for an even i, 44 for
    // an odd one), and v.len() (1) — over 100 even and 100 odd rounds.
    if (acc != 200 * 3 + 100 * (45 + 44)) { return 99; }
    return __rc_underflow_count();
}`

func TestX86_64MapArrayValueColumnReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, mapArrayValueColumnSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (the values are arrays of heap strings), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("map string[] value column leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	_, stderr, code = runLeakCheckX86_64(t, mapArrayValueColumnBoundSrc)
	if code != 0 {
		t.Fatalf("bound get_or: exit=%d, want 0", code)
	}
	allocs, frees, live = parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("bound get_or of a string[] value leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	if _, code := compileAndRunX86_64FreeOn(t, mapArrayValueColumnUnderflowSrc); code != 0 {
		t.Errorf("map string[] value reclaim: code=%d (99=wrong value, >0=over-release)", code)
	}
	small := mustRunX86_64FreeOn(t, mapArrayValueColumnBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, mapArrayValueColumnBumpSrc("5000"))
	if small != large {
		t.Errorf("map bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64MapArrayValueColumnReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, mapArrayValueColumnSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (the values are arrays of heap strings), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("map string[] value column leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	_, stderr, code = runLeakCheckArm64(t, mapArrayValueColumnBoundSrc)
	if code != 0 {
		t.Fatalf("bound get_or: exit=%d, want 0", code)
	}
	allocs, frees, live = parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("bound get_or of a string[] value leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	if _, code := compileAndRunArm64FreeOn(t, mapArrayValueColumnUnderflowSrc); code != 0 {
		t.Errorf("map string[] value reclaim: code=%d (99=wrong value, >0=over-release)", code)
	}
	small := mustRunArm64FreeOn(t, mapArrayValueColumnBumpSrc("50"))
	large := mustRunArm64FreeOn(t, mapArrayValueColumnBumpSrc("5000"))
	if small != large {
		t.Errorf("map bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestWASMMapArrayValueColumnReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, mapArrayValueColumnBumpSrc("50"))
	large := runWasm(t, mapArrayValueColumnBumpSrc("5000"))
	if small != large {
		t.Errorf("map bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, mapArrayValueColumnUnderflowSrc); got != 0 {
		t.Errorf("map string[] value reclaim: code=%d (99=wrong value, >0=over-release)", got)
	}
}
