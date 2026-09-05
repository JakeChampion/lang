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
