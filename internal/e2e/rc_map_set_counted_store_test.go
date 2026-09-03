// #4399 sink 5 — `m.insert(k, v)` and the `Map { k: v }` literal are
// COUNTED stores: emitMapSetRetains incs an aliased string key and an
// aliased value the map both retains and drops (arrays, deep-dropped
// boxes, strings), and appendMapDropChain walks those columns at the
// map's drop. computeFreeEligible's Map_set arm therefore no longer
// escape-taints the key / value SOURCE locals, so they reclaim at scope
// exit and the map's own column release balances the store's retain.
//
// Three contracts, on x86-64, arm64 and wasm:
//   - BALANCED: FERN_LEAKCHECK reads allocs == frees, live 0, on the
//     natives — pre-change every key / value local built from a fresh
//     concat or literal was stranded, one block per insert; wasm has no
//     leak counter, so it rides the __heap_bump_bytes() high-water probe,
//     flat under reclaim and linear under a leak.
//   - CORRECT: reading the entry back after the source local is
//     reclaimable yields the right value (the store's retain kept it alive).
//   - NO OVER-RELEASE: __rc_underflow_count() stays 0.
//
// The insert-only shapes (`_A`) are what the leak balance measures. A
// lookup by the same key local (`m.get_or(k, …)`) is deliberately kept
// to the underflow legs: on the native single-word ABI the #4174 blanket
// string-argument taint fires for a builtin's arguments too, so the key
// is re-tainted there and its release masked — a separate cost recorded
// in docs/rc-log/2026-09-03-map-set-counted-store.md, not this sink's.
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// mapSetCountedSrcs are the insert-only shapes: one fresh key / value
// local per call, stored, then read through the map only by length. Each
// returns 0 on a correct sum with no rc underflow.
var mapSetCountedSrcs = map[string]string{
	"string value": `import "core/map";
function work(i: i32): i32 {
    var stem: string = "alpha";
    var k: string = stem + "-key-long";
    var v: string = stem + "-value-long";
    var m: Map[string, string] = map_new(4);
    m = m.insert(k, v);
    return m.len() + k.len() + v.len();
}
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 31) { return 99; }
    return __rc_underflow_count();
}`,
	"array value": `import "core/map";
function work(i: i32): i32 {
    var stem: string = "alpha";
    var k: string = stem + "-key-long";
    var v: i32[] = [i, i + 1, i + 2];
    var m: Map[string, i32[]] = map_new(4);
    m = m.insert(k, v);
    return m.len() + k.len() + v.len();
}
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 18) { return 99; }
    return __rc_underflow_count();
}`,
	"struct value": `import "core/map";
struct Pt { name: string, x: i32 }
function work(i: i32): i32 {
    var stem: string = "alpha";
    var k: string = stem + "-key-long";
    var v: Pt = Pt { name: stem + "-point-name", x: i };
    var m: Map[string, Pt] = map_new(4);
    m = m.insert(k, v);
    return m.len() + k.len() + v.name.len();
}
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 31) { return 99; }
    return __rc_underflow_count();
}`,
	"map literal": `import "core/map";
function work(i: i32): i32 {
    var stem: string = "alpha";
    var k: string = stem + "-key-long";
    var v: i32[] = [i, i + 1, i + 2];
    var m: Map[string, i32[]] = Map { k: v };
    return m.len() + k.len() + v.len();
}
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 18) { return 99; }
    return __rc_underflow_count();
}`,
}

// mapSetCountedBumpSrc is the "array value" shape as a bump-growth probe
// over n iterations — the wasm instrument.
func mapSetCountedBumpSrc(n string) string {
	return `import "core/map";
function work(i: i32): i32 {
    var stem: string = "alpha";
    var k: string = stem + "-key-long";
    var v: i32[] = [i, i + 1, i + 2];
    var m: Map[string, i32[]] = map_new(4);
    m = m.insert(k, v);
    return m.len() + k.len() + v.len();
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + work(i); i = i + 1; }
    if (acc != ` + n + ` * 18) { return 99; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// mapSetReadBackSrcs read the stored entry back through the map after the
// store — the CORRECT half, with the source locals still live and then
// released — and return 0 only on the right values with no underflow.
var mapSetReadBackSrcs = map[string]string{
	"string value": `import "core/map";
function work(i: i32): i32 {
    var stem: string = "alpha";
    var k: string = stem + "-key-long";
    var v: string = stem + "-value-long";
    var m: Map[string, string] = map_new(4);
    m = m.insert(k, v);
    var got: string = m.get_or(k, "");
    return got.len() + i - i;
}
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 16) { return 99; }
    return __rc_underflow_count();
}`,
	"array value": `import "core/map";
function work(i: i32): i32 {
    var stem: string = "alpha";
    var k: string = stem + "-key-long";
    var v: i32[] = [i, i + 1, i + 2];
    var m: Map[string, i32[]] = map_new(4);
    m = m.insert(k, v);
    var got: i32[] = m.get_or(k, []);
    return got[2] - i + v.len();
}
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 5) { return 99; }
    return __rc_underflow_count();
}`,
	"struct value": `import "core/map";
struct Pt { name: string, x: i32 }
function work(i: i32): i32 {
    var stem: string = "alpha";
    var k: string = stem + "-key-long";
    var v: Pt = Pt { name: stem + "-point-name", x: i };
    var m: Map[string, Pt] = map_new(4);
    m = m.insert(k, v);
    var got: Pt = m.get_or(k, v);
    return got.name.len() + got.x - i;
}
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200 * 16) { return 99; }
    return __rc_underflow_count();
}`,
	// The uncounted remainder must not over-release: a nested-Map value is
	// held by the outer map's column uncounted, so its source stays
	// tainted and the inner map is still readable through the outer one
	// after the source's scope ends. Read through a match binding rather
	// than a bound local: an uncounted kind-1 alias bound to a local takes
	// the flat exit dec and underflows on its own, before and after this
	// change (docs/rc-log/2026-09-03-map-set-counted-store.md).
	"nested map value stays live": `import "core/map";
function build(i: i32): Map[i32, Map[i32, i32]] {
    var inner: Map[i32, i32] = map_new(2);
    inner = inner.insert(i, i + 1);
    var outer: Map[i32, Map[i32, i32]] = map_new(2);
    outer = outer.insert(i, inner);
    return outer;
}
function work(i: i32): i32 {
    var outer: Map[i32, Map[i32, i32]] = build(i);
    match (outer.get(i)) {
        Some(inner) => { return inner.get_or(i, -1) - i; },
        None => { return -1000; },
    }
}
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200) { return 99; }
    return __rc_underflow_count();
}`,
}

func TestX86_64MapSetCountedStoreBalanced(t *testing.T) {
	for name, src := range mapSetCountedSrcs {
		t.Run(name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, src)
			if code != 0 {
				t.Fatalf("exit=%d, want 0 (99 = wrong sum, other = rc underflow)", code)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("expected allocations (the key / value are heap blocks), got 0")
			}
			if allocs != frees || live != 0 {
				t.Errorf("map store sources leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
			}
		})
	}
	for name, src := range mapSetReadBackSrcs {
		t.Run("read back "+name, func(t *testing.T) {
			if got := mustRunX86_64FreeOn(t, src); got != 0 {
				t.Errorf("want exit 0, got %d (99 = wrong value, other = rc underflow)", got)
			}
		})
	}
}

func TestArm64MapSetCountedStoreBalanced(t *testing.T) {
	for name, src := range mapSetCountedSrcs {
		t.Run(name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, src)
			if code != 0 {
				t.Fatalf("exit=%d, want 0 (99 = wrong sum, other = rc underflow)", code)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("expected allocations (the key / value are heap blocks), got 0")
			}
			if allocs != frees || live != 0 {
				t.Errorf("map store sources leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
			}
		})
	}
	for name, src := range mapSetReadBackSrcs {
		t.Run("read back "+name, func(t *testing.T) {
			if got := mustRunArm64FreeOn(t, src); got != 0 {
				t.Errorf("want exit 0, got %d (99 = wrong value, other = rc underflow)", got)
			}
		})
	}
}

func TestWasmMapSetCountedStoreBalanced(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, mapSetCountedBumpSrc("50"))
	large := runWasm(t, mapSetCountedBumpSrc("5000"))
	// Flat: the pre-change baseline grew 32 B per call here.
	if small != large {
		t.Errorf("map store bump should be bounded (sources reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	for name, src := range mapSetCountedSrcs {
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, src); got != 0 {
				t.Errorf("want exit 0, got %d (99 = wrong sum, other = rc underflow)", got)
			}
		})
	}
	for name, src := range mapSetReadBackSrcs {
		t.Run("read back "+name, func(t *testing.T) {
			if got := runWasm(t, src); got != 0 {
				t.Errorf("want exit 0, got %d (99 = wrong value, other = rc underflow)", got)
			}
		})
	}
}
