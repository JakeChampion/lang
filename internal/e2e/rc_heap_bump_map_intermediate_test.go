package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A map RETURNED FROM A CALL — `var m = mk(i)` / a discarded `mk(i);` where
// `mk` is the canonical cow-threaded builder (`var m = map_new(8); m =
// m.insert(..); return m;`) — leaked its handle + buffer every iteration on
// both compilers (#4357's map-intermediate slice), while the same map built
// INLINE in the loop bounded (rc_heap_bump_map_reinit_test.go).
//
// Root cause: computeFreshLocals only admitted a local used exclusively in
// return position, so the `m.insert(..)` receiver reads tainted `m`, `return
// m` failed exprNoParamEscape, and returnsNoParamEscape[mk] stayed false —
// the call result was never freeEligible at the binding, and the discarded
// form's emitOwnedTempStackDrop had no Map routing at all (flat rc_dec).
//
// The fix is the COW self-reassign carve-out: a `m = m.insert(..)` statement
// (isSelfMapMutation, receiver occurring exactly once) keeps `m` fresh — the
// cow mutator returns the same fresh handle or a fresh copy of it — PROVIDED
// every stored key/value is itself escape-free against the declared K/V slot
// types. Map_set stores are UNCOUNTED and the fresh-credit reclaim
// (emitMapSlotDrop) deep-frees the value column, so a param-derived store
// must disqualify the builder — the negatives below pin exactly that.
func mapIntermediateBoundBumpSrc(n string) string {
	return `import "core/map";
function mk(k: i32): Map[i32, i32] {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    m = m.insert(k + 1, k * 3);
    return m;
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var m: Map[i32, i32] = mk(i);
        acc = acc + m.get_or(i, 0);
        i = i + 1;
    }
    if (acc < 0) { return 121; }
    var g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
}

func mapIntermediateDiscardBumpSrc(n string) string {
	return `import "core/map";
function mk(k: i32): Map[i32, i32] {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m;
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    var g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
}

// Value-correct + over-release-free for the credited path: the reclaimed maps
// must have carried the right entries, and the underflow detector must stay 0.
const mapIntermediateUnderflowSrc = `import "core/map";
function mk(k: i32): Map[i32, i32] {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    m = m.insert(k + 1, k * 3);
    return m;
}
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 300) {
        var m: Map[i32, i32] = mk(i);
        acc = acc + m.get_or(i, 0) + m.get_or(i + 1, 0);
        i = i + 1;
    }
    if (acc != 5 * (300 * 299 / 2)) { return 121; }
    return __rc_underflow_count();
}`

// SOUNDNESS NEGATIVE 1 — cow-threading a PARAM receiver must stay unproven:
// grow's rc==1 in-place insert returns the CALLER's handle, so crediting it
// as fresh would free `base` out from under the loop. The carve-out excludes
// params structurally (they are not Var decls); this pins that `base` stays
// intact and nothing over-releases.
const mapIntermediateParamReceiverSrc = `import "core/map";
function grow(m: Map[i32, i32], k: i32): Map[i32, i32] {
    m = m.insert(k, k * 7);
    return m;
}
function main(): i32 {
    var base: Map[i32, i32] = map_new(8);
    base = base.insert(1, 11);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 300) {
        var g: Map[i32, i32] = grow(base, i + 2);
        acc = acc + g.get_or(1, 0);
        i = i + 1;
    }
    if (base.get_or(1, 0) != 11) { return 121; }
    if (acc != 300 * 11) { return 120; }
    return __rc_underflow_count();
}`

// SOUNDNESS NEGATIVE 2 — a builder storing a PARAM-DERIVED pointer value must
// stay unproven: Map_set stores are uncounted and emitMapSlotDrop deep-frees
// the value column, so crediting mk would free the caller's `keep` buffer.
// The carve-out's per-argument escape-free check (against the declared V slot
// type) rejects the bare param ident; `keep` must survive every iteration.
const mapIntermediatePtrValueParamSrc = `import "core/map";
function mk(xs: i32[], k: i32): Map[i32, i32[]] {
    var m: Map[i32, i32[]] = map_new(8);
    m = m.insert(k, xs);
    return m;
}
function main(): i32 {
    var keep: i32[] = [7, 8, 9];
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 300) {
        var t: Map[i32, i32[]] = mk(keep, i);
        var got: i32[] = t.get_or(i, []);
        acc = acc + got.len();
        i = i + 1;
    }
    if (keep[0] != 7 || keep[1] != 8 || keep[2] != 9 || keep.len() != 3) { return 121; }
    if (acc != 900) { return 120; }
    return __rc_underflow_count();
}`

func runMapIntermediateChecks(t *testing.T, run func(*testing.T, string) int) {
	t.Helper()
	for _, tc := range []struct {
		name string
		src  func(string) string
	}{
		{"bound", mapIntermediateBoundBumpSrc},
		{"discarded", mapIntermediateDiscardBumpSrc},
	} {
		small := run(t, tc.src("50"))
		large := run(t, tc.src("5000"))
		if small != large {
			t.Errorf("%s map-intermediate bump should be bounded: N=50 -> %d, N=5000 -> %d", tc.name, small, large)
		}
		if small == 0 {
			t.Errorf("%s: expected a non-zero bounded high-water, got 0", tc.name)
		}
		if small >= 119 {
			t.Errorf("%s: growth guard tripped (%d) — probe scaling needs adjusting", tc.name, small)
		}
	}
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"credited-underflow", mapIntermediateUnderflowSrc},
		{"param-receiver-negative", mapIntermediateParamReceiverSrc},
		{"ptr-value-param-negative", mapIntermediatePtrValueParamSrc},
	} {
		if code := run(t, tc.src); code != 0 {
			t.Errorf("%s: code=%d (121/120=value mismatch, >0=over-release)", tc.name, code)
		}
	}
}

func TestX86_64MapIntermediateReclaim(t *testing.T) {
	runMapIntermediateChecks(t, mustRunX86_64FreeOn)
}

func TestArm64MapIntermediateReclaim(t *testing.T) {
	runMapIntermediateChecks(t, mustRunArm64FreeOn)
}

func TestWASMMapIntermediateReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	runMapIntermediateChecks(t, func(t *testing.T, src string) int {
		return runWasm(t, src)
	})
}
