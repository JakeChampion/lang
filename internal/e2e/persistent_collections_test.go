package e2e

import "testing"

// The persistent collections (#6794) — std/ordmap, std/ordset, std/pmap,
// std/pset, std/pvec — are generic enum trees whose correctness is
// backend-sensitive in one specific way: an update must rebuild the path
// to the changed node and share the rest, and a compiled backend that
// takes the in-place path on a node another binding still holds rewrites
// a value that was supposed to be immutable. The interpreter never does
// that, so each module is pinned on every backend that compiles it
// (x86-64 / wasm / arm64-qemu, each skipping when its toolchain is
// absent) with an exit-coded program: a snapshot is taken, the original
// is updated through every mutating verb, and the snapshot is checked
// entry by entry — a distinct exit code per failed check, 42 on success.

const ordmapPersistProg = `
import "std/ordmap";
import "std/option";
function main(): i32 {
    var m: ordmap.OrdMap[i32, i32] = ordmap.ordmap_new();
    var i: i32 = 0;
    while (i < 600) { m = m.insert((i * 7919) % 600, i); i = i + 1; }
    if (m.len() != 600 || !m.is_valid()) { return 1; }
    var snap: ordmap.OrdMap[i32, i32] = m;
    i = 0;
    while (i < 600) { m = m.insert(i, i + 1000); i = i + 1; }
    i = 0;
    while (i < 600) {
        if (snap.get_or((i * 7919) % 600, -1) != i) { return 2; }
        if (m.get_or(i, -1) != i + 1000) { return 3; }
        i = i + 1;
    }
    var half: ordmap.OrdMap[i32, i32] = m.filter((k: i32, v: i32) => k % 2 == 0);
    if (half.len() != 300 || !half.is_valid()) { return 4; }
    if (half.union(snap).len() != 600 || snap.difference(half).len() != 300 || snap.intersection(half).len() != 300) { return 5; }
    i = 0;
    while (i < 600) { m = m.remove(i); if (!m.is_valid()) { return 6; } i = i + 1; }
    if (m.len() != 0 || snap.len() != 600 || !snap.is_valid()) { return 7; }
    var ks: i32[] = snap.keys();
    if (ks.len() != 600 || ks[0] != 0 || ks[599] != 599) { return 8; }
    if (snap.index_of(300) != 300 || snap.key_at(5).unwrap_or(-1) != 5) { return 9; }
    if (snap.fold(0, (a: i32, k: i32, v: i32) => a + k) != 179700) { return 10; }
    var s: ordmap.OrdMap[string, i32] = ordmap.ordmap_new();
    s = s.insert("b", 2);
    s = s.insert("a", 1);
    var ss: ordmap.OrdMap[string, i32] = s;
    s = s.remove("a");
    if (ss.len() != 2 || s.len() != 1 || !ss.contains("a") || s.contains("a")) { return 11; }
    return 42;
}
`

const pmapPersistProg = `
import "std/pmap";
import "std/option";
import "core/cmp";
@derive(cmp.Eq)
struct Coarse { bucket: i32, id: i32 }
impl cmp.Hash for Coarse {
    function hash(self: Self): i32 { return self.bucket; }
}
function main(): i32 {
    var m: pmap.PMap[i32, i32] = pmap.pmap_new();
    var i: i32 = 0;
    while (i < 1500) { m = m.insert((i * 7919) % 1500, i); i = i + 1; }
    if (m.len() != 1500 || !m.is_valid()) { return 1; }
    var snap: pmap.PMap[i32, i32] = m;
    i = 0;
    while (i < 1500) { m = m.insert(i, i + 1000); i = i + 1; }
    i = 0;
    while (i < 1500) {
        if (snap.get_or((i * 7919) % 1500, -1) != i) { return 2; }
        if (m.get_or(i, -1) != i + 1000) { return 3; }
        i = i + 1;
    }
    var half: pmap.PMap[i32, i32] = m.filter((k: i32, v: i32) => k % 2 == 0);
    if (half.len() != 750 || !half.is_valid()) { return 4; }
    if (half.union(snap).len() != 1500 || snap.difference(half).len() != 750 || snap.intersection(half).len() != 750) { return 5; }
    i = 0;
    while (i < 1500) { m = m.remove(i); if (!m.is_valid()) { return 6; } i = i + 1; }
    if (m.len() != 0 || snap.len() != 1500 || !snap.is_valid()) { return 7; }
    if (snap.keys().len() != 1500 || snap.fold(0, (a: i32, k: i32, v: i32) => a + k) != 1124250) { return 8; }
    var s: pmap.PMap[string, i32] = pmap.pmap_new();
    i = 0;
    while (i < 200) { s = s.insert("k" + i.to_string(), i); i = i + 1; }
    var ss: pmap.PMap[string, i32] = s;
    s = s.remove("k7");
    if (ss.len() != 200 || s.len() != 199 || !ss.contains("k7") || s.contains("k7") || s.get_or("k8", -1) != 8) { return 9; }
    var c: pmap.PMap[Coarse, i32] = pmap.pmap_new();
    i = 0;
    while (i < 120) { c = c.insert(Coarse { bucket: i % 4, id: i }, i); i = i + 1; }
    var cs: pmap.PMap[Coarse, i32] = c;
    i = 0;
    while (i < 120) { if (i % 3 == 0) { c = c.remove(Coarse { bucket: i % 4, id: i }); } i = i + 1; }
    if (c.len() != 80 || cs.len() != 120 || !c.is_valid() || !cs.contains(Coarse { bucket: 0, id: 0 }) || c.contains(Coarse { bucket: 0, id: 0 })) { return 10; }
    if (c.get_or(Coarse { bucket: 1, id: 1 }, -1) != 1) { return 11; }
    return 42;
}
`

const pvecPersistProg = `
import "std/pvec";
import "std/option";
function main(): i32 {
    var v: pvec.PVec[i32] = pvec.pvec_new();
    var i: i32 = 0;
    while (i < 2100) { v = v.append(i); if (!v.is_valid()) { return 1; } i = i + 1; }
    var snap: pvec.PVec[i32] = v;
    i = 0;
    while (i < 2100) { v = v.with(i, i * 2); i = i + 1; }
    if (!v.is_valid() || !snap.is_valid()) { return 2; }
    i = 0;
    while (i < 2100) {
        if (snap.get_or(i, -1) != i) { return 3; }
        if (v.get_or(i, -1) != i * 2) { return 4; }
        i = i + 1;
    }
    var w: pvec.PVec[i32] = v;
    i = 0;
    while (i < 2100) { w = w.pop(); if (!w.is_valid() || w.len() != 2099 - i) { return 5; } i = i + 1; }
    if (!w.is_empty() || v.len() != 2100 || v.last().unwrap_or(-1) != 4198) { return 6; }
    var arr: i32[] = snap.to_array();
    if (arr.len() != 2100 || arr[2099] != 2099 || snap.fold(0, (a: i32, x: i32) => a + x) != 2203950) { return 7; }
    var sl: pvec.PVec[i32] = snap.slice(10, 20);
    if (sl.len() != 10 || sl.concat(sl).len() != 20 || sl.get_or(9, -1) != 19) { return 8; }
    var s: pvec.PVec[string] = pvec.pvec_new();
    i = 0;
    while (i < 70) { s = s.append("s" + i.to_string()); i = i + 1; }
    var ss: pvec.PVec[string] = s;
    s = s.with(3, "changed");
    if (ss.get_or(3, "") != "s3" || s.get_or(3, "") != "changed" || s.get_or(69, "") != "s69") { return 9; }
    return 42;
}
`

const setsPersistProg = `
import "std/ordset";
import "std/pset";
import "std/option";
function main(): i32 {
    var a: ordset.OrdSet[i32] = ordset.ordset_of([5, 3, 9, 3, 1]);
    var b: ordset.OrdSet[i32] = ordset.ordset_of([3, 4, 5]);
    if (a.len() != 4 || !a.is_valid() || !a.contains(3) || a.contains(4)) { return 1; }
    var arr: i32[] = a.to_array();
    if (arr.len() != 4 || arr[0] != 1 || arr[3] != 9) { return 2; }
    if (a.union(b).len() != 5 || a.intersection(b).len() != 2 || a.difference(b).len() != 2) { return 3; }
    if (a.min().unwrap_or(-1) != 1 || a.max().unwrap_or(-1) != 9 || a.at(2).unwrap_or(-1) != 5 || a.index_of(9) != 3) { return 4; }
    var snap: ordset.OrdSet[i32] = a;
    a = a.remove(3);
    if (a.len() != 3 || snap.len() != 4 || !snap.contains(3) || a.contains(3)) { return 5; }
    var s: pset.PSet[string] = pset.pset_of(["a", "b", "a", "c"]);
    if (s.len() != 3 || !s.is_valid() || !s.contains("b") || s.contains("z")) { return 6; }
    var before: pset.PSet[string] = s;
    s = s.add("d");
    s = s.remove("a");
    if (s.len() != 3 || before.len() != 3 || !before.contains("a") || s.contains("a")) { return 7; }
    var t: pset.PSet[string] = pset.pset_of(["c", "d", "e"]);
    if (s.union(t).len() != 4 || s.intersection(t).len() != 2 || s.difference(t).len() != 1) { return 8; }
    var big: pset.PSet[i32] = pset.pset_new();
    var i: i32 = 0;
    while (i < 3000) { big = big.add(i % 1000); i = i + 1; }
    if (big.len() != 1000 || !big.is_valid()) { return 9; }
    return 42;
}
`

var persistentProgs = []struct {
	name string
	src  string
}{
	{"ordmap", ordmapPersistProg},
	{"pmap", pmapPersistProg},
	{"pvec", pvecPersistProg},
	{"sets", setsPersistProg},
}

func TestPersistentCollectionsInterp(t *testing.T) {
	for _, p := range persistentProgs {
		t.Run(p.name, func(t *testing.T) {
			if got := runInterpExit(t, p.src); got != 42 {
				t.Fatalf("interp got %d, want 42", got)
			}
		})
	}
}

func TestPersistentCollectionsX86_64(t *testing.T) {
	for _, p := range persistentProgs {
		t.Run(p.name, func(t *testing.T) {
			if _, got := compileAndRunX86_64(t, p.src); got != 42 {
				t.Fatalf("x86-64 got %d, want 42", got)
			}
		})
	}
}

func TestPersistentCollectionsWasm(t *testing.T) {
	for _, p := range persistentProgs {
		t.Run(p.name, func(t *testing.T) {
			if got := compileAndRunWasmbinMain(t, p.src); got != 42 {
				t.Fatalf("wasm got %d, want 42", got)
			}
		})
	}
}

func TestPersistentCollectionsArm64(t *testing.T) {
	for _, p := range persistentProgs {
		t.Run(p.name, func(t *testing.T) {
			if _, got := compileAndRunArm64(t, p.src); got != 42 {
				t.Fatalf("arm64 got %d, want 42", got)
			}
		})
	}
}
