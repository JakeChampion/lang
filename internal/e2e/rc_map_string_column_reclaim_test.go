package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #2704 class 1 — "map keys and non-array map values are never reclaimed".
//
// The column walks existed but did not release on the native single-word ABI:
// `__drop_map_str_keys` / `__drop_map_str_values` (and the overwrite pre-drop
// next to them) dec'd each entry with a bare `__fern_rc_dec`, which takes the
// count to zero and returns nothing to the allocator, so every heap key and
// value a map ever held was stranded. Three per-lookup temporaries leaked
// alongside it on one ABI or the other: get_or's boxed fallback cell, the
// retained get_or result, and the key temp of a get / get_or.
//
// The probes below are the issue's own shapes with the strings pushed past the
// SSO inline threshold — an inline-packed string has no buffer, which is what
// made an earlier round of these measurements read as clean.
//
// FERN_LEAKCHECK is the instrument on the natives (exact block + byte balance);
// wasm has no leak counter, so it rides the __heap_bump_bytes() high-water
// probe, which is flat under reclaim and linear under a leak.

// mapStringColumnSrc exercises the whole map surface a string-keyed,
// string-valued map has — build, three flavours of lookup, and a drop — with
// every key and value a freshly allocated (non-inline) buffer.
const mapStringColumnSrc = `import "core/map";
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 500) {
        var stem: string = "alpha";
        var m: Map[string, string] = map_new(4);
        m = m.insert(stem + "-key-long", stem + "-value-long");
        match (m.get(stem + "-key-long")) { Some(v) => { acc = acc + v.len(); }, None => {} }
        if (m.has(stem + "-key-long")) { acc = acc + 1; }
        acc = acc + m.get_or(stem + "-key-long", stem + "-fallback!").len();
        i = i + 1;
    }
    if (acc < 0) { return 1; }
    return 0;
}`

// The same shape as a bump-growth probe: `n` iterations of build-lookup-drop,
// returning the bytes the bump cursor advanced. Bounded iff every column and
// every lookup temporary is reclaimed and reused.
func mapStringColumnBumpSrc(n string) string {
	return `import "core/map";
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var stem: string = "alpha";
        var m: Map[string, string] = map_new(4);
        m = m.insert(stem + "-key-long", stem + "-value-long");
        match (m.get(stem + "-key-long")) { Some(v) => { acc = acc + v.len(); }, None => {} }
        acc = acc + m.get_or(stem + "-key-long", stem + "-fallback!").len();
        i = i + 1;
    }
    if (acc < 0) { return acc; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Value correctness + zero over-release on the same surface. 0 iff every
// lookup read the right string AND no reclaim dec'd past zero — the direction
// leakcheck cannot see.
const mapStringColumnUnderflowSrc = `import "core/map";
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var stem: string = "alpha";
        var m: Map[string, string] = map_new(4);
        m = m.insert(stem + "-key-long", stem + "-value-long");
        var got: string = m.get_or(stem + "-key-long", "");
        acc = acc + got.len();
        match (m.get(stem + "-key-long")) { Some(v) => { acc = acc + v.len(); }, None => { acc = acc + 100; } }
        i = i + 1;
    }
    // "alpha-value-long" is 16 chars, read twice per iteration.
    if (acc != 200 * 32) { return 99; }
    return __rc_underflow_count();
}`

// A wide-scalar value boxes its get_or fallback into a cell on EVERY ABI —
// including x86-64, which has no __fern_cell_free — so that cell is its own
// per-lookup leak, 8 B a time.
const mapWideValueFallbackSrc = `import "core/map";
function main(): i32 {
    var i: i32 = 0;
    var acc: i64 = 0;
    while (i < 500) {
        var m: Map[i32, i64] = map_new(4);
        m = m.insert(7, 1234567890123);
        acc = acc + m.get_or(7, 0);
        i = i + 1;
    }
    if (acc != 500 * 1234567890123) { return 99; }
    return 0;
}`

// An OVERWRITE under an alias — the shape that says where the release of a
// replaced value belongs. An IR-side pre-drop runs BEFORE the set's own
// __map_cow_inplace, so a second handle over the same buffer still names the
// value it would release: freeing it there is an uncounted-alias free (no rc
// detector fires, and the fault lands wherever the freelist next hands the
// block out, which on x86-64 was a SIGSEGV), and gating the pre-drop off
// instead reclaims nothing. Since #8421 the release lives in __map_dec_value,
// which the set reaches AFTER the COW, so it is sole-owner-correct by
// construction and runs on the aliased path too.
//
// 0 iff both handles read back what they should AND nothing over-released;
// the byte census is asserted separately, against the baseline below.
//
// ONE ENTRY, which is all this particular probe pins. The second, untouched
// entry — where both copies' drop walks used to free the same cell and buffer
// — is mapAliasedTwoEntrySrc below, green since the string value column gained
// its own claim (#8354).
const mapAliasedOverwriteSrc = `import "core/map";
function mk(): i32 {
    var stem: string = "a";
    var m: Map[string, string] = map_new(8);
    var k: string = stem + "-key-long-one";
    m = m.insert(k, stem + "-value-long-one");
    var snap: Map[string, string] = m;
    m = m.insert(k, stem + "-value-long-two");
    var ok: i32 = 0;
    if (snap.get_or(k, "") == "a-value-long-one") { ok = ok + 1; }
    if (m.get_or(k, "") == "a-value-long-two") { ok = ok + 2; }
    if (snap.len() == 1) { ok = ok + 4; }
    if (m.len() == 1) { ok = ok + 8; }
    return ok;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { t = t + mk(); i = i + 1; }
    if (t != 200 * 15) { return 97; }
    return __rc_underflow_count();
}`

// The DROP-time half, and #8354's own repro. A second entry that is never
// written still had both copies' drop walks free its value cell and its
// buffer, because __memcpy'ing the kv buffer shares the value column and
// nothing claimed it. Unfixed this crashes on every backend — wasm traps on
// an out-of-bounds read at a string's own bytes read as a pointer, arm64 dies
// on a signal, x86-64 returns the wrong answer.
//
// 0 iff all four reads through both handles are right AND nothing
// over-released; the byte census is asserted separately, against the
// two-key baseline below.
const mapAliasedTwoEntrySrc = `import "core/map";
function mk(): i32 {
    var stem: string = "a";
    var m: Map[string, string] = map_new(8);
    var k1: string = stem + "-key-long-one";
    var k2: string = stem + "-key-long-two";
    m = m.insert(k1, stem + "-value-long-one");
    m = m.insert(k2, stem + "-value-long-untouched");
    var snap: Map[string, string] = m;
    m = m.insert(k1, stem + "-value-long-two");
    var ok: i32 = 0;
    if (snap.get_or(k1, "") == "a-value-long-one") { ok = ok + 1; }
    if (m.get_or(k1, "") == "a-value-long-two") { ok = ok + 2; }
    if (snap.get_or(k2, "") == "a-value-long-untouched") { ok = ok + 4; }
    if (m.get_or(k2, "") == "a-value-long-untouched") { ok = ok + 8; }
    return ok;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { t = t + mk(); i = i + 1; }
    if (t != 200 * 15) { return 97; }
    return __rc_underflow_count();
}`

// The two probes above with the ALIAS and the OVERWRITE taken out, and
// nothing else changed: same number of maps per round, same keys, same key
// lengths, same reads. They are what the census is measured against.
//
// On arm64 and wasm32 both sides are 0 and the comparison is incidental. It
// earns its keep on x86-64, where a third defect sits in the same program:
// #8277 strands the map's KEY buffer once per map on any read with an aliased
// key, which is present in the baseline too. Pinning an absolute number here
// would bank that leak; pinning the DIFFERENCE says exactly what #8421 is
// about — that aliasing an overwrite costs nothing — and starts failing the
// moment it costs something again.
const mapOverwriteCensusBaselineSrc = `import "core/map";
function mk(): i32 {
    var stem: string = "a";
    var m: Map[string, string] = map_new(8);
    var k: string = stem + "-key-long-one";
    m = m.insert(k, stem + "-value-long-two");
    var ok: i32 = 0;
    if (m.get_or(k, "") == "a-value-long-two") { ok = ok + 3; }
    if (m.len() == 1) { ok = ok + 12; }
    return ok;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { t = t + mk(); i = i + 1; }
    if (t != 200 * 15) { return 97; }
    return __rc_underflow_count();
}`

const mapTwoEntryCensusBaselineSrc = `import "core/map";
function mk(): i32 {
    var stem: string = "a";
    var m: Map[string, string] = map_new(8);
    var k1: string = stem + "-key-long-one";
    var k2: string = stem + "-key-long-two";
    m = m.insert(k1, stem + "-value-long-two");
    m = m.insert(k2, stem + "-value-long-untouched");
    var ok: i32 = 0;
    if (m.get_or(k1, "") == "a-value-long-two") { ok = ok + 3; }
    if (m.get_or(k2, "") == "a-value-long-untouched") { ok = ok + 12; }
    return ok;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { t = t + mk(); i = i + 1; }
    if (t != 200 * 15) { return 97; }
    return __rc_underflow_count();
}`

// assertAliasedOverwriteCosts nothing: `probe` (which aliases and overwrites)
// must strand no more bytes than `baseline` (which does neither), and must
// allocate strictly more — without that second half a probe that stopped
// building the map at all would pass.
func assertAliasedOverwriteFreeOfCharge(t *testing.T, name string, run func(string) (int64, int64, int64), probe, baseline string) {
	t.Helper()
	pa, pf, pl := run(probe)
	ba, _, bl := run(baseline)
	if pl != bl {
		t.Errorf("%s: aliasing the overwrite strands %d bytes, the same shape without it strands %d — the difference is #8421's", name, pl, bl)
	}
	if pa <= ba {
		t.Errorf("%s: probe allocated %d, baseline %d — the probe must do strictly more work, or the census is vacuous", name, pa, ba)
	}
	if pa == 0 {
		t.Errorf("%s: no allocations at all; the probe is not exercising heap strings", name)
	}
	_ = pf
}

func TestX86_64MapStringColumnReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, mapStringColumnSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (keys and values are heap strings), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("map string columns leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	if _, code := compileAndRunX86_64FreeOn(t, mapStringColumnUnderflowSrc); code != 0 {
		t.Errorf("map string reclaim: code=%d (99=wrong value, >0=over-release)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, mapWideValueFallbackSrc); code != 0 {
		t.Errorf("wide-value get_or: code=%d (99=wrong value)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, mapAliasedOverwriteSrc); code != 0 {
		t.Errorf("aliased overwrite: code=%d (97=wrong value, >0=over-release, signal=the freed value came back)", code)
	}
	small := mustRunX86_64FreeOn(t, mapStringColumnBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, mapStringColumnBumpSrc("5000"))
	if small != large {
		t.Errorf("map bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, mapAliasedTwoEntrySrc); code != 0 {
		t.Errorf("aliased two-entry: code=%d (97=wrong value, >0=over-release, signal=a shared value column was freed twice)", code)
	}
	x86Census := func(src string) (int64, int64, int64) {
		t.Helper()
		_, stderr, code := runLeakCheckX86_64(t, src)
		if code != 0 {
			t.Fatalf("census probe exit=%d, want 0", code)
		}
		return parseLeakCheckLine(t, stderr)
	}
	assertAliasedOverwriteFreeOfCharge(t, "aliased overwrite", x86Census, mapAliasedOverwriteSrc, mapOverwriteCensusBaselineSrc)
	assertAliasedOverwriteFreeOfCharge(t, "aliased two-entry", x86Census, mapAliasedTwoEntrySrc, mapTwoEntryCensusBaselineSrc)
}

func TestArm64MapStringColumnReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, mapStringColumnSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (keys and values are heap strings), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("map string columns leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	if _, code := compileAndRunArm64FreeOn(t, mapStringColumnUnderflowSrc); code != 0 {
		t.Errorf("map string reclaim: code=%d (99=wrong value, >0=over-release)", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, mapWideValueFallbackSrc); code != 0 {
		t.Errorf("wide-value get_or: code=%d (99=wrong value)", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, mapAliasedOverwriteSrc); code != 0 {
		t.Errorf("aliased overwrite: code=%d (97=wrong value, >0=over-release, signal=the freed value came back)", code)
	}
	small := mustRunArm64FreeOn(t, mapStringColumnBumpSrc("50"))
	large := mustRunArm64FreeOn(t, mapStringColumnBumpSrc("5000"))
	if small != large {
		t.Errorf("map bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, mapAliasedTwoEntrySrc); code != 0 {
		t.Errorf("aliased two-entry: code=%d (97=wrong value, >0=over-release, signal=a shared value column was freed twice)", code)
	}
	// arm64 boxes its strings, so there is no #8277 residual underneath and
	// the census is absolute: every byte back.
	for _, probe := range []struct {
		name string
		src  string
	}{
		{"aliased overwrite", mapAliasedOverwriteSrc},
		{"aliased two-entry", mapAliasedTwoEntrySrc},
	} {
		_, stderr, code := runLeakCheckArm64(t, probe.src)
		if code != 0 {
			t.Fatalf("%s census: exit=%d, want 0", probe.name, code)
		}
		a, f, live := parseLeakCheckLine(t, stderr)
		if a == 0 {
			t.Fatalf("%s census: no allocations, the probe is not exercising heap strings", probe.name)
		}
		if a != f || live != 0 {
			t.Errorf("%s leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", probe.name, a, f, live)
		}
	}
}

func TestWASMMapStringColumnReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, mapStringColumnBumpSrc("50"))
	large := runWasm(t, mapStringColumnBumpSrc("5000"))
	if small != large {
		t.Errorf("map bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, mapStringColumnUnderflowSrc); got != 0 {
		t.Errorf("map string reclaim: code=%d (99=wrong value, >0=over-release)", got)
	}
	if got := runWasm(t, mapWideValueFallbackSrc); got != 0 {
		t.Errorf("wide-value get_or: code=%d (99=wrong value)", got)
	}
	if got := runWasm(t, mapAliasedOverwriteSrc); got != 0 {
		t.Errorf("aliased overwrite: code=%d (97=wrong value, >0=over-release)", got)
	}
	if got := runWasm(t, mapAliasedTwoEntrySrc); got != 0 {
		t.Errorf("aliased two-entry: code=%d (97=wrong value, >0=over-release)", got)
	}
	// As on arm64: boxed strings, no #8277 underneath, absolute census.
	for _, probe := range []struct {
		name string
		src  string
	}{
		{"aliased overwrite", mapAliasedOverwriteSrc},
		{"aliased two-entry", mapAliasedTwoEntrySrc},
	} {
		_, stderr, code := runLeakCheckWasm(t, probe.src, false)
		if code != 0 {
			t.Fatalf("%s census: exit=%d, want 0", probe.name, code)
		}
		a, f, live := parseLeakCheckLine(t, stderr)
		if a == 0 {
			t.Fatalf("%s census: no allocations, the probe is not exercising heap strings", probe.name)
		}
		if a != f || live != 0 {
			t.Errorf("%s leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", probe.name, a, f, live)
		}
	}
}

// #2704 classes 2 and 3 — the generic-enum payload and the non-uniform nested
// struct field. Both were closed by earlier slices (substituteEnumDecl's
// recursive ParamType substitution; the per-type __drop_* recursion), and the
// issue asked for the leak-count assertion that keeps them closed. The class-2
// shape is the one its own audit named: an Option[Item] built by a helper and
// consumed by a match ON THE CALL RESULT, which needs the fresh-scrutinee
// reclaim as well as the substituted payload drop.

const genericEnumPayloadLeakSrc = `struct Item { name: string, v: i32 }
function mk(i: i32, sfx: string): Option[Item] {
    return Some(Item { name: "item-with-a-long-name" + sfx, v: i });
}
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 500) {
        var sfx: string = "s";
        match (mk(i, sfx)) {
            Some(it) => { acc = acc + it.v + it.name.len(); },
            None => {},
        }
        i = i + 1;
    }
    if (acc < 0) { return 1; }
    return 0;
}`

const nestedNonUniformFieldLeakSrc = `struct Inner { data: i32[], tag: string }
struct Outer { inner: Inner, id: i32 }
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 500) {
        var stem: string = "tag";
        var o: Outer = Outer { inner: Inner { data: [1, 2, 3], tag: stem + "-of-this-inner" }, id: i };
        acc = acc + o.inner.data.len() + o.inner.tag.len();
        i = i + 1;
    }
    if (acc < 0) { return 1; }
    return 0;
}`

func TestX86_64GenericEnumPayloadNoLeak(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, genericEnumPayloadLeakSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (box + Item + name), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("generic enum payload leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestX86_64NestedNonUniformFieldNoLeak(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, nestedNonUniformFieldLeakSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (Outer + Inner + data + tag), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("nested non-uniform fields leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestArm64GenericEnumPayloadNoLeak(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, genericEnumPayloadLeakSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("generic enum payload leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestArm64NestedNonUniformFieldNoLeak(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, nestedNonUniformFieldLeakSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("nested non-uniform fields leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}
