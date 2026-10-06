package e2ecompiler

import "testing"

// A map whose value column holds maps (#11071). A map box is counted on every
// backend, so the column is a column of boxes like any other: a read retains
// the inner map, an overwrite or delete releases the one it displaces, and the
// outer map's last unit releases every inner one.
//
// Each program returns 0 only on the right answers with no rc underflow, and
// every leg reads a balanced FERN_LEAKCHECK census. An integer- or
// string-keyed outer map is core/map's; a struct-keyed one is the backend
// runtime's, holding core/map maps, so both representations carry the column.
var mapOfMapPrograms = []struct {
	name string
	src  string
}{
	// TestWasmMapSetCountedStoreBalanced's nested-map program, which the
	// typed lowering refused whole.
	{"read-through-match", `import "core/map";
function build(i: i32): Map[i32, Map[i32, i32]] {
    let inner: Map[i32, i32] = map_new(2);
    inner = inner.insert(i, i + 1);
    let outer: Map[i32, Map[i32, i32]] = map_new(2);
    outer = outer.insert(i, inner);
    return outer;
}
function work(i: i32): i32 {
    let outer: Map[i32, Map[i32, i32]] = build(i);
    match (outer.get(i)) {
        Some(inner) => { return inner.get_or(i, -1) - i; },
        None => { return -1000; },
    }
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    if (acc != 200) { return 99; }
    return __rc_underflow_count();
}`},
	// Insert, read an inner map out, mutate it and store it back under a new
	// key, overwrite, delete, iterate, snapshot the values, drop the outer map.
	{"round-trip", `import "core/map";

function adjacency(n: i32): Map[i32, Map[i32, i32]] {
    let g: Map[i32, Map[i32, i32]] = map_new(4);
    let i: i32 = 0;
    while (i < n) {
        let row: Map[i32, i32] = map_new(2);
        row = row.insert(i + 1, i * 10);
        row = row.insert(i + 2, i * 20);
        g = g.insert(i, row);
        i = i + 1;
    }
    return g;
}

function mutate(g: Map[i32, Map[i32, i32]]): Map[i32, Map[i32, i32]] {
    let row: Map[i32, i32] = g.get_or(1, map_new(1));
    row = row.insert(99, 7);
    g = g.insert(100, row);
    return g;
}

function weight(g: Map[i32, Map[i32, i32]]): i32 {
    let total: i32 = 0;
    for (k, row) in g {
        total = total + k + row.len();
        for (_, w) in row {
            total = total + w;
        }
    }
    return total;
}

function rows(g: Map[i32, Map[i32, i32]]): i32 {
    let total: i32 = 0;
    for row in g.values() {
        total = total + row.len();
    }
    return total;
}

function round(n: i32): i32 {
    let g: Map[i32, Map[i32, i32]] = adjacency(n);
    g = mutate(g);
    let one: i32 = 0;
    match (g.get(1)) {
        Some(r) => { one = r.len(); },
        None => { return -1; },
    }
    let hundred: i32 = 0;
    match (g.get(100)) {
        Some(r) => { hundred = r.get_or(99, -1); },
        None => { return -2; },
    }
    let fresh: Map[i32, i32] = map_new(1);
    fresh = fresh.insert(5, 5);
    g = g.insert(0, fresh);
    let (g2, existed) = g.without(2);
    if (!existed) { return -4; }
    let lit: Map[string, Map[i32, i32]] = Map { "a": fresh };
    let la: i32 = 0;
    match (lit.get("a")) {
        Some(r) => { la = r.get_or(5, -1); },
        None => { return -3; },
    }
    return one * 1000 + hundred * 100 + la * 10 + g2.len() + weight(g2) + rows(g2);
}

function main(): i32 {
    let i: i32 = 0;
    while (i < 20) {
        if (round(4) != 3036) { return 98; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`},
	// A struct-keyed outer map over core/map maps, and a core/map outer map
	// over struct-keyed ones.
	{"struct-keyed", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Coord { x: i32, y: i32 }

function round(n: i32): i32 {
    let g: Map[Coord, Map[i32, i32]] = map_new(2);
    let h: Map[i32, Map[Coord, i32]] = map_new(2);
    let i: i32 = 0;
    while (i < n) {
        let row: Map[i32, i32] = map_new(2);
        row = row.insert(i, i * 3);
        g = g.insert(Coord { x: i, y: i }, row);
        let col: Map[Coord, i32] = map_new(2);
        col = col.insert(Coord { x: i, y: 0 }, i * 5);
        h = h.insert(i, col);
        i = i + 1;
    }
    let r1: Map[i32, i32] = g.get_or(Coord { x: 1, y: 1 }, map_new(1));
    r1 = r1.insert(50, 50);
    g = g.insert(Coord { x: 9, y: 9 }, r1);
    let c2: Map[Coord, i32] = map_new(1);
    h = h.insert(0, c2);
    let total: i32 = 0;
    for (k, row) in g {
        total = total + k.x + row.len();
        for (_, w) in row {
            total = total + w;
        }
    }
    for row in g.values() {
        total = total + row.len() * 3;
    }
    for col in h.values() {
        total = total + col.len() * 7;
    }
    match (g.get(Coord { x: 1, y: 1 })) {
        Some(r) => { total = total + r.len() * 1000; },
        None => { return -1; },
    }
    let (g2, existed) = g.without(Coord { x: 0, y: 0 });
    if (!existed) { return -2; }
    return total + g2.len() * 100000;
}

function main(): i32 {
    let i: i32 = 0;
    while (i < 10) {
        if (round(3) != 301108) { return 98; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`},
	// An insert into a shared outer map copies it first; the copy holds a unit
	// of every inner map and the original keeps answering its own entries.
	{"shared-outer", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Coord { x: i32, y: i32 }

function row(i: i32): Map[i32, i32] {
    let r: Map[i32, i32] = map_new(2);
    return r.insert(i, i * 2);
}

function keyed(n: i32): i32 {
    let g: Map[Coord, Map[i32, i32]] = map_new(2);
    let i: i32 = 0;
    while (i < n) {
        g = g.insert(Coord { x: i, y: 0 }, row(i));
        i = i + 1;
    }
    let before: Map[Coord, Map[i32, i32]] = g;
    g = g.insert(Coord { x: 99, y: 0 }, row(99));
    return g.len() * 100 + before.len();
}

function routed(n: i32): i32 {
    let g: Map[i32, Map[i32, i32]] = map_new(2);
    let i: i32 = 0;
    while (i < n) {
        g = g.insert(i, row(i));
        i = i + 1;
    }
    let before: Map[i32, Map[i32, i32]] = g;
    g = g.insert(99, row(99));
    let inner: i32 = 0;
    match (before.get(1)) {
        Some(r) => { inner = r.get_or(1, -1); },
        None => { return -1; },
    }
    return g.len() * 100 + before.len() + inner * 10000;
}

function main(): i32 {
    let i: i32 = 0;
    while (i < 10) {
        if (keyed(3) != 403) { return 98; }
        if (routed(3) != 20403) { return 97; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`},
}

func checkMapOfMaps(t *testing.T, target string) {
	cli := buildSelfHostCLI(t)
	for _, p := range mapOfMapPrograms {
		t.Run(p.name, func(t *testing.T) {
			stderr, exit := cli.exitOf(t, p.src, target, "FERN_LEAKCHECK=1")
			if exit != 0 {
				t.Fatalf("exit = %d, want 0 (97/98/99 = wrong answer, other = rc underflow)\n%s", exit, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostMapOfMapsX86_64(t *testing.T) {
	checkMapOfMaps(t, "x86-64-linux")
	cli := buildSelfHostCLI(t)
	for _, p := range mapOfMapPrograms {
		t.Run("sanitize/"+p.name, func(t *testing.T) {
			stderr, exit := cli.exitOf(t, p.src, "x86-64-linux", "FERN_SANITIZE=1")
			if exit != 0 {
				t.Fatalf("exit = %d under the sanitizer, want 0\n%s", exit, stderr)
			}
		})
	}
}

func TestSelfHostMapOfMapsArm64(t *testing.T) {
	checkMapOfMaps(t, "arm64-linux")
}

func TestSelfHostMapOfMapsWasm(t *testing.T) {
	checkMapOfMaps(t, "wasm32-wasi")
}
