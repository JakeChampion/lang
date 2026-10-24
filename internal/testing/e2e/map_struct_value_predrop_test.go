package e2e

import "testing"

const mapAliasedStructOverwriteSrc = `import "core/map";
struct Box { name: string }
function mk(): i32 {
    let stem: string = "a";
    let m: Map[i32, Box] = map_new(8);
    m = m.insert(1, Box { name: stem + "-value-long-one" });
    let snap: Map[i32, Box] = m;
    m = m.insert(1, Box { name: stem + "-value-long-two" });
    let ok: i32 = 0;
    match (snap.get(1)) { Some(b) => { if (b.name == "a-value-long-one") { ok = ok + 1; } }, None => {} }
    match (m.get(1)) { Some(b) => { if (b.name == "a-value-long-two") { ok = ok + 2; } }, None => {} }
    if (snap.len() == 1) { ok = ok + 4; }
    if (m.len() == 1) { ok = ok + 8; }
    return ok;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
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

const mapAliasedStructNoOverwriteSrc = `import "core/map";
struct Box2 { name: string }
function mk(): i32 {
    let stem: string = "a";
    let m: Map[i32, Box2] = map_new(8);
    m = m.insert(1, Box2 { name: stem + "-value-long-one" });
    m = m.insert(2, Box2 { name: stem + "-value-long-two" });
    let snap: Map[i32, Box2] = m;
    m = m.insert(3, Box2 { name: stem + "-value-long-three" });
    let ok: i32 = 0;
    match (snap.get(1)) { Some(b) => { if (b.name == "a-value-long-one") { ok = ok + 1; } }, None => {} }
    match (m.get(2)) { Some(b) => { if (b.name == "a-value-long-two") { ok = ok + 2; } }, None => {} }
    match (m.get(3)) { Some(b) => { if (b.name == "a-value-long-three") { ok = ok + 4; } }, None => {} }
    if (snap.len() == 2 && m.len() == 3) { ok = ok + 8; }
    return ok;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { t = t + mk(); i = i + 1; }
    if (t != 200 * 15) { return 97; }
    return __rc_underflow_count();
}`

const mapAliasedDeepStructSrc = `import "core/map";
struct Inner { tag: string }
struct Deep { name: string, xs: i32[], inner: Inner }
function mk(): i32 {
    let stem: string = "a";
    let m: Map[i32, Deep] = map_new(8);
    m = m.insert(1, Deep { name: stem + "-value-long-one", xs: [1, 2, 3], inner: Inner { tag: stem + "-tag-long-one" } });
    m = m.insert(2, Deep { name: stem + "-value-long-two", xs: [4, 5, 6], inner: Inner { tag: stem + "-tag-long-two" } });
    let snap: Map[i32, Deep] = m;
    m = m.insert(3, Deep { name: stem + "-value-long-three", xs: [7, 8, 9], inner: Inner { tag: stem + "-tag-long-three" } });
    let ok: i32 = 0;
    match (snap.get(1)) { Some(d) => { if (d.name == "a-value-long-one" && d.xs.len() == 3 && d.inner.tag == "a-tag-long-one") { ok = ok + 1; } }, None => {} }
    match (m.get(2)) { Some(d) => { if (d.name == "a-value-long-two" && d.xs.len() == 3 && d.inner.tag == "a-tag-long-two") { ok = ok + 2; } }, None => {} }
    match (m.get(3)) { Some(d) => { if (d.name == "a-value-long-three" && d.xs.len() == 3 && d.inner.tag == "a-tag-long-three") { ok = ok + 4; } }, None => {} }
    if (snap.len() == 2 && m.len() == 3) { ok = ok + 8; }
    return ok;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
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
