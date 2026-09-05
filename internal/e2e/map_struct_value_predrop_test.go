package e2e

// The kind-4 (boxed struct / enum value) overwrite pre-drop, and why it needs
// the same sole-owner gate the string pre-drops carry.
//
// `m.insert(k, v)` over an existing key deep-drops the superseded value inline,
// BEFORE the set reaches its own `__map_cow_inplace`. A second handle over the
// same buffer therefore still names that value when it is freed, and #8354
// leaves the struct value column shared on a COW copy exactly as it leaves the
// string one.
//
// Ungated, `var snap = m; m = m.insert(k, s)` returned the WRONG value on all
// three backends over 200 rounds while `FERN_LEAKCHECK=1` reported
// allocs=2400 frees=2400 live_bytes=0. Every free is at rc 1 and the box is
// recycled under the reader, so the heap balances exactly and no detector
// fires — the reason this shipped. The answer is the only instrument that
// sees it, which is what these probes read.
//
// The gate trades that for a leak: with it, the same run reports
// allocs=2400 frees=2000 (2 blocks a round), the #8354 residual on a column a
// copy still shares. A wrong answer is not a price worth paying for it.

import "testing"

const mapAliasedStructOverwriteSrc = `import "core/map";
struct Box { name: string }
function mk(): i32 {
    var stem: string = "a";
    var m: Map[i32, Box] = map_new(8);
    m = m.insert(1, Box { name: stem + "-value-long-one" });
    var snap: Map[i32, Box] = m;
    m = m.insert(1, Box { name: stem + "-value-long-two" });
    var ok: i32 = 0;
    match (snap.get(1)) { Some(b) => { if (b.name == "a-value-long-one") { ok = ok + 1; } }, None => {} }
    match (m.get(1)) { Some(b) => { if (b.name == "a-value-long-two") { ok = ok + 2; } }, None => {} }
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

func TestX86_64MapAliasedStructOverwrite(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, mapAliasedStructOverwriteSrc); code != 0 {
		t.Errorf("aliased struct overwrite: code=%d (97=the pre-drop freed what snap reads, >0=over-release)", code)
	}
}

func TestArm64MapAliasedStructOverwrite(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, mapAliasedStructOverwriteSrc); code != 0 {
		t.Errorf("aliased struct overwrite: code=%d (97=the pre-drop freed what snap reads, >0=over-release)", code)
	}
}

func TestWASMMapAliasedStructOverwrite(t *testing.T) {
	if got := runWasm(t, mapAliasedStructOverwriteSrc); got != 0 {
		t.Errorf("aliased struct overwrite: code=%d (97=the pre-drop freed what snap reads, >0=over-release)", got)
	}
}

// The struct value column IS claimed on a COW copy, and one inc per entry is
// the whole claim — the slot is a single pointer to an rc'd box whose
// generated deep drop frees at its last reference, so no per-field walk is
// needed in the inc direction. `__map_own_copied_cols`' `retainVals` arm
// already covers kind 4.
//
// This is pinned because the opposite was written down: the comment in
// `__map_own_copied_cols` claimed the struct column was "left shared ... for
// want of a way to claim it here", #8420 was filed on that reading, and both
// were wrong. A byte census is the instrument that settles it, and unlike the
// aliased-overwrite probes above this shape can assert one — there is no
// overwrite, so #8421's leak is not in the way.
//
// The insert AFTER the alias is what makes the probe able to fail. Aliasing
// alone does not copy anything: `__map_own_copied_cols` runs from
// `__map_cow_inplace`, which only fires when a handle at rc>1 is MUTATED. A
// NEW key is the mutation that reaches it while displacing no value, so the
// claim is exercised and the census stays assertable.
//
// `Deep` carries the shapes a per-field walk would have been needed for: a
// string, an rc-tracked array, and a nested struct with its own string.
const mapAliasedStructNoOverwriteSrc = `import "core/map";
struct Box2 { name: string }
function mk(): i32 {
    var stem: string = "a";
    var m: Map[i32, Box2] = map_new(8);
    m = m.insert(1, Box2 { name: stem + "-value-long-one" });
    m = m.insert(2, Box2 { name: stem + "-value-long-two" });
    var snap: Map[i32, Box2] = m;
    m = m.insert(3, Box2 { name: stem + "-value-long-three" });
    var ok: i32 = 0;
    match (snap.get(1)) { Some(b) => { if (b.name == "a-value-long-one") { ok = ok + 1; } }, None => {} }
    match (m.get(2)) { Some(b) => { if (b.name == "a-value-long-two") { ok = ok + 2; } }, None => {} }
    match (m.get(3)) { Some(b) => { if (b.name == "a-value-long-three") { ok = ok + 4; } }, None => {} }
    if (snap.len() == 2 && m.len() == 3) { ok = ok + 8; }
    return ok;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { t = t + mk(); i = i + 1; }
    if (t != 200 * 15) { return 97; }
    return __rc_underflow_count();
}`

const mapAliasedDeepStructSrc = `import "core/map";
struct Inner { tag: string }
struct Deep { name: string, xs: i32[], inner: Inner }
function mk(): i32 {
    var stem: string = "a";
    var m: Map[i32, Deep] = map_new(8);
    m = m.insert(1, Deep { name: stem + "-value-long-one", xs: [1, 2, 3], inner: Inner { tag: stem + "-tag-long-one" } });
    m = m.insert(2, Deep { name: stem + "-value-long-two", xs: [4, 5, 6], inner: Inner { tag: stem + "-tag-long-two" } });
    var snap: Map[i32, Deep] = m;
    m = m.insert(3, Deep { name: stem + "-value-long-three", xs: [7, 8, 9], inner: Inner { tag: stem + "-tag-long-three" } });
    var ok: i32 = 0;
    match (snap.get(1)) { Some(d) => { if (d.name == "a-value-long-one" && d.xs.len() == 3 && d.inner.tag == "a-tag-long-one") { ok = ok + 1; } }, None => {} }
    match (m.get(2)) { Some(d) => { if (d.name == "a-value-long-two" && d.xs.len() == 3 && d.inner.tag == "a-tag-long-two") { ok = ok + 2; } }, None => {} }
    match (m.get(3)) { Some(d) => { if (d.name == "a-value-long-three" && d.xs.len() == 3 && d.inner.tag == "a-tag-long-three") { ok = ok + 4; } }, None => {} }
    if (snap.len() == 2 && m.len() == 3) { ok = ok + 8; }
    return ok;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { t = t + mk(); i = i + 1; }
    if (t != 200 * 15) { return 97; }
    return __rc_underflow_count();
}`

func TestX86_64MapStructValueColumnClaimed(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"box", mapAliasedStructNoOverwriteSrc},
		{"deep", mapAliasedDeepStructSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, tc.src); code != 0 {
				t.Fatalf("code=%d (97=wrong value, >0=over-release, signal=the column was freed twice)", code)
			}
			_, stderr, ec := runLeakCheckX86_64(t, tc.src)
			if ec != 0 {
				t.Fatalf("leakcheck exit=%d", ec)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("expected allocations (the values are heap boxes), got 0")
			}
			if allocs != frees || live != 0 {
				t.Errorf("the struct value column is claimed, so an aliased copy must balance: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
			}
		})
	}
}

func TestArm64MapStructValueColumnClaimed(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"box", mapAliasedStructNoOverwriteSrc},
		{"deep", mapAliasedDeepStructSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, tc.src); code != 0 {
				t.Fatalf("code=%d (97=wrong value, >0=over-release, signal=the column was freed twice)", code)
			}
			_, stderr, ec := runLeakCheckArm64(t, tc.src)
			if ec != 0 {
				t.Fatalf("leakcheck exit=%d", ec)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs != frees || live != 0 {
				t.Errorf("the struct value column is claimed, so an aliased copy must balance: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
			}
		})
	}
}

func TestWASMMapStructValueColumnClaimed(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"box", mapAliasedStructNoOverwriteSrc},
		{"deep", mapAliasedDeepStructSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != 0 {
				t.Errorf("code=%d (97=wrong value, >0=over-release)", got)
			}
		})
	}
}
