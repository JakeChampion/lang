package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A map whose key is a string, a 32-bit integer or a boolean, and whose value
// is one of those or a box, runs on core/map's hash table under the typed
// lowering (ssarc.routed_map, #9608). These cases pin what the routing has to
// keep: the whole Map surface, copy-on-write under an alias, negative keys
// (which cross into core/map's usize slot and must be equal slots however they
// were computed), u32 values past 2^31, boolean columns, and the units a string
// or box column holds — on every target, with every allocation returned.

// routedMapSurfaceSrc exercises every routed op: construction, insert with and
// without an alias, get / get_or / has, without, keys / values, the cursor,
// a map in a struct field, in an array, captured by a closure, and cleared.
const routedMapSurfaceSrc = `import "core/map";
import "std/i32";

struct Holder { m: Map[i32, i32], tag: i32 }

function build(n: i32): Map[i32, i32] {
    var m: Map[i32, i32] = Map {};
    var i: i32 = 0;
    while (i < n) { m = m.insert(i + 50, (i + 50) * 3); i = i + 1; }
    return m;
}

function sum_iter(m: Map[i32, i32]): i32 {
    var s: i32 = 0;
    for (k, v) in m { s = s + k * 7 + v; }
    return s;
}

function main(): i32 {
    var m: Map[i32, i32] = build(1000);
    var alias: Map[i32, i32] = m;
    alias = alias.insert(5000, 1);
    var s: i32 = m.len() * 100000 + alias.len();
    s = s + m.get_or(50, 0) + m.get_or(123456, -9);
    match (m.get(51)) { Some(v) => { s = s + v; }, None => { s = s - 1; } }
    match (m.get(99999)) { Some(v) => { s = s + v; }, None => { s = s - 1; } }
    var r: (Map[i32, i32], boolean) = m.without(50);
    var m2: Map[i32, i32] = r.0;
    if (r.1) { s = s + 11; }
    if (m.has(50) && !m2.has(50)) { s = s + 13; }
    s = s + sum_iter(m2);
    var ks: i32[] = m2.keys();
    var vs: i32[] = m2.values();
    var ki: i32 = 0;
    while (ki < ks.len()) { s = s + ks[ki] - vs[ki]; ki = ki + 1; }
    var h: Holder = Holder { m: build(10), tag: 4 };
    s = s + h.m.len() + h.tag;
    var hs: Holder[] = [h, Holder { m: m2, tag: 1 }];
    s = s + hs[1].m.len();
    var cap: Map[i32, i32] = build(5);
    var f = (x: i32): i32 => { return cap.get_or(x, 0) + cap.len(); };
    s = s + f(52);
    var e: Map[i32, i32] = m.cleared();
    s = s + e.len();
    for (k, v) in e { s = s + k + v; }
    print(s.to_string());
    return 0;
}
`

// routedMapNegativeKeysSrc inserts keys computed in a loop and looks them up
// by literal: the two forms reach the slot through different instructions.
const routedMapNegativeKeysSrc = `import "core/map";
import "std/i32";
function main(): i32 {
    var m: Map[i32, i32] = Map {};
    var i: i32 = 0;
    while (i < 1000) { m = m.insert(i - 50, (i - 50) * 3); i = i + 1; }
    var got: string = "none";
    match (m.get(-49)) { Some(v) => { got = v.to_string(); }, None => {} }
    print(m.get_or(-50, 7777).to_string() + " " + got + " " + m.get_or(949, 0).to_string() + " " + m.has(-51).to_string());
    return 0;
}
`

// routedMapCowSrc: an insert or a without through an alias copies, and the
// original is untouched.
const routedMapCowSrc = `import "core/map";
import "std/i32";
function build(n: i32): Map[i32, i32] {
    var m: Map[i32, i32] = Map {};
    var i: i32 = 0;
    while (i < n) { m = m.insert(i, i * 2); i = i + 1; }
    return m;
}
function main(): i32 {
    var a: Map[i32, i32] = build(100);
    var b: Map[i32, i32] = a;
    b = b.insert(7, 1000);
    var c: Map[i32, i32] = a;
    var r: (Map[i32, i32], boolean) = c.without(3);
    c = r.0;
    print(a.get_or(7, 0).to_string() + " " + b.get_or(7, 0).to_string() + " " + a.has(3).to_string() + " "
        + c.has(3).to_string() + " " + a.len().to_string() + " " + b.len().to_string() + " " + c.len().to_string());
    return 0;
}
`

// routedMapU32BoolSrc: a u32 column past 2^31 and boolean columns.
const routedMapU32BoolSrc = `import "core/map";
import "std/i32";
function main(): i32 {
    var big: Map[u32, u32] = Map {};
    big = big.insert(4000000000 as u32, 3000000000 as u32);
    big = big.insert(1 as u32, 2 as u32);
    var sum: u64 = 0 as u64;
    for (k, v) in big { sum = sum + (k as u64) + (v as u64); }
    var flags: Map[boolean, boolean] = Map {};
    flags = flags.insert(true, false);
    flags = flags.insert(false, true);
    flags = flags.insert(true, true);
    var trues: i32 = 0;
    for v in flags.values() { if (v) { trues = trues + 1; } }
    for k in flags.keys() { if (k) { trues = trues + 10; } }
    print((big.get_or(4000000000 as u32, 0 as u32) == (3000000000 as u32)).to_string() + " "
        + (sum == (7000000003 as u64)).to_string() + " " + flags.len().to_string() + " " + trues.to_string());
    return 0;
}
`

// routedMapStringKeysSrc: a string key column holds one unit of each key. An
// overwrite keeps the equal key already there and releases the arriving one;
// `without` releases the key it removes; a copy under an alias takes a unit of
// every key; the map's last release walks them all.
const routedMapStringKeysSrc = `import "core/map";
import "std/i32";
function key(i: i32): string { return "k" + i.to_string(); }
function build(n: i32): Map[string, i32] {
    var m: Map[string, i32] = Map {};
    var i: i32 = 0;
    while (i < n) { m = m.insert(key(i), i * 3); i = i + 1; }
    return m;
}
function main(): i32 {
    var m: Map[string, i32] = build(200);
    m = m.insert(key(5), 999);
    m = m.insert("lit", 7);
    var alias: Map[string, i32] = m;
    alias = alias.insert(key(1000), 1);
    alias = alias.insert(key(6), 66);
    var s: i32 = m.len() * 1000 + alias.len();
    s = s + m.get_or(key(5), 0) + m.get_or("nope", -9) + alias.get_or(key(6), 0) + m.get_or(key(6), 0);
    if (m.has("lit")) { s = s + 1; }
    match (m.get(key(7))) { Some(v) => { s = s + v; }, None => { s = s - 1; } }
    var r: (Map[string, i32], boolean) = m.without(key(8));
    var m2: Map[string, i32] = r.0;
    if (r.1 && !m2.has(key(8)) && m.has(key(8))) { s = s + 13; }
    var r2: (Map[string, i32], boolean) = m2.without("absent");
    m2 = r2.0;
    var kl: i32 = 0;
    for k in m2.keys() { kl = kl + k.len(); }
    var it: i32 = 0;
    for (k, v) in m2 { it = it + k.len() + v; }
    var e: Map[string, i32] = m2.cleared();
    print(s.to_string() + " " + kl.to_string() + " " + it.to_string() + " " + e.len().to_string());
    return 0;
}
`

// routedMapStringValuesSrc: string value columns hold their strings in the
// slots, so an overwrite, a delete, an alias's copy and the last drop each
// release exactly the values they own, and a get hands out its own reference.
const routedMapStringValuesSrc = `import "core/map";
import "std/i32";

function build(n: i32): Map[string, string] {
    var m: Map[string, string] = Map {};
    var i: i32 = 0;
    while (i < n) { m = m.insert("k" + i.to_string(), "v" + (i * 3).to_string()); i = i + 1; }
    return m;
}

function main(): i32 {
    var m: Map[string, string] = build(300);
    m = m.insert("k7", "over" + "written");
    var alias: Map[string, string] = m;
    alias = alias.insert("k8", "alias" + "only");
    var out: string = m.get_or("k7", "none") + " " + alias.get_or("k8", "none") + " " + m.get_or("k8", "none");
    out = out + " " + m.get_or("zz", "miss" + "ed");
    match (m.get("k9")) { Some(v) => { out = out + " " + v; }, None => { out = out + " none"; } }
    var r: (Map[string, string], boolean) = m.without("k10");
    var m2: Map[string, string] = r.0;
    out = out + " " + r.1.to_string() + " " + m.has("k10").to_string() + " " + m2.has("k10").to_string();
    var total: i32 = 0;
    for (k, v) in m2 { total = total + k.len() + v.len(); }
    var vs: string[] = m2.values();
    out = out + " " + total.to_string() + " " + vs.len().to_string();
    var by_id: Map[i32, string] = Map {};
    var j: i32 = 0;
    while (j < 50) { by_id = by_id.insert(j % 10, "n" + j.to_string()); j = j + 1; }
    var r2: (Map[i32, string], boolean) = by_id.without(3);
    by_id = r2.0;
    out = out + " " + by_id.get_or(4, "") + " " + by_id.len().to_string();
    var e: Map[string, string] = m.cleared();
    out = out + " " + e.len().to_string();
    print(out);
    return 0;
}
`

// routedMapBoxValuesSrc: array, record and enum value columns hold one unit of
// each box. core/map retains a box on every read and on a copy, and the
// lowering releases the box an overwrite supersedes, a delete removes and the
// last drop still holds, the last through the type's own release.
const routedMapBoxValuesSrc = `import "core/map";
import "std/i32";

struct Rec { name: string, n: i32 }
enum Shape { Circle(i32), Label(string), Empty }

function build(n: i32): Map[string, i32[]] {
    var m: Map[string, i32[]] = Map {};
    var i: i32 = 0;
    while (i < n) { m = m.insert("k" + i.to_string(), [i, i * 2, i * 3]); i = i + 1; }
    return m;
}

function shape_n(s: Shape): i32 {
    match (s) { Circle(r) => { return r; }, Label(t) => { return t.len(); }, Empty => { return 0; } }
}

function main(): i32 {
    var m: Map[string, i32[]] = build(200);
    m = m.insert("k7", [70, 71]);
    var alias: Map[string, i32[]] = m;
    alias = alias.insert("k8", [1, 2, 3, 4, 5]);
    var out: string = m.get_or("k7", []).len().to_string() + " " + alias.get_or("k8", []).len().to_string() + " " + m.get_or("k8", []).len().to_string();
    out = out + " " + m.get_or("zz", [9, 9, 9, 9]).len().to_string();
    match (m.get("k9")) { Some(v) => { out = out + " " + v[2].to_string(); }, None => { out = out + " none"; } }
    var r: (Map[string, i32[]], boolean) = m.without("k10");
    var m2: Map[string, i32[]] = r.0;
    out = out + " " + r.1.to_string() + " " + m.has("k10").to_string() + " " + m2.has("k10").to_string();
    var total: i32 = 0;
    for (k, v) in m2 { total = total + k.len() + v.len() + v[0]; }
    var vs: i32[][] = m2.values();
    out = out + " " + total.to_string() + " " + vs.len().to_string() + " " + vs[3][1].to_string();

    var recs: Map[i32, Rec] = Map {};
    var j: i32 = 0;
    while (j < 50) { recs = recs.insert(j % 10, Rec { name: "n" + j.to_string(), n: j }); j = j + 1; }
    var r2: (Map[i32, Rec], boolean) = recs.without(3);
    recs = r2.0;
    var hold: Map[i32, Rec] = recs;
    recs = recs.insert(4, Rec { name: "four" + "!", n: 4 });
    out = out + " " + recs.get_or(4, Rec { name: "", n: 0 }).name + " " + hold.get_or(4, Rec { name: "", n: 0 }).name + " " + recs.len().to_string();
    for (k, v) in recs { total = total + v.n + v.name.len(); }

    var shapes: Map[string, Shape] = Map {};
    shapes = shapes.insert("a", Circle(3));
    shapes = shapes.insert("b", Label("hello" + "!"));
    shapes = shapes.insert("c", Empty);
    shapes = shapes.insert("b", Label("bye"));
    var sn: i32 = 0;
    for (k, v) in shapes { sn = sn + shape_n(v); }
    for v in shapes.values() { sn = sn + shape_n(v); }
    out = out + " " + sn.to_string() + " " + total.to_string();
    var e: Map[string, i32[]] = m.cleared();
    out = out + " " + e.len().to_string();
    print(out);
    return 0;
}
`

// routedMapTupleValuesSrc: a tuple value is a box like a record's.
const routedMapTupleValuesSrc = `import "core/map";
import "std/i32";
function main(): i32 {
    var m: Map[string, (i32, string)] = Map {};
    var i: i32 = 0;
    while (i < 30) { m = m.insert("k" + (i % 7).to_string(), (i, "v" + i.to_string())); i = i + 1; }
    var alias: Map[string, (i32, string)] = m;
    alias = alias.insert("k1", (100, "over" + "ride"));
    var r: (Map[string, (i32, string)], boolean) = m.without("k2");
    var m2: Map[string, (i32, string)] = r.0;
    var total: i32 = 0;
    for (k, v) in m2 { total = total + v.0 + v.1.len(); }
    for v in alias.values() { total = total + v.0; }
    var hit: (i32, string) = m.get_or("k3", (0, ""));
    var got: string = "none";
    match (alias.get("k1")) { Some(v) => { got = v.1; }, None => {} }
    print(total.to_string() + " " + hit.1 + " " + got + " " + m2.len().to_string() + " " + r.1.to_string());
    return 0;
}
`

// routedMapDynValuesSrc: a dyn value's box is whichever concrete it holds —
// a boxed i32 or string, a record, an enum — and each is retained and
// released through the one dyn release.
const routedMapDynValuesSrc = `import "core/map";
import "std/i32";
trait Show { function show(self: Self): i32; }
struct Dot { r: i32 }
enum Op { Add(i32), Neg }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
impl Show for Dot { function show(self: Self): i32 { return self.r * 2; } }
impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } }
function main(): i32 {
    var m: Map[string, dyn Show] = Map {};
    m = m.insert("a", 41);
    m = m.insert("b", "hello" + "!");
    m = m.insert("c", Dot { r: 5 });
    m = m.insert("d", Add(7));
    var alias: Map[string, dyn Show] = m;
    alias = alias.insert("b", Neg);
    var r: (Map[string, dyn Show], boolean) = m.without("c");
    var total: i32 = 0;
    for (k, v) in r.0 { total = total + v.show(); }
    for v in alias.values() { total = total + v.show(); }
    match (m.get("b")) { Some(v) => { total = total + v.show() * 100; }, None => {} }
    print(total.to_string());
    return 0;
}
`

// routedMapTwoDynValuesSrc: two dyn types release through helpers of their
// own; keyed alike, one type's release would leave the other's concretes.
const routedMapTwoDynValuesSrc = `import "core/map";
import "std/i32";
trait Aa { function a(self: Self): i32; }
trait Bb { function b(self: Self): i32; }
struct Xa { s: string, n: i32 }
struct Yb { t: string, u: string }
impl Aa for Xa { function a(self: Self): i32 { return self.s.len() + self.n; } }
impl Bb for Yb { function b(self: Self): i32 { return self.t.len() + self.u.len(); } }
function main(): i32 {
    var ma: Map[string, dyn Aa] = Map {};
    var mb: Map[string, dyn Bb] = Map {};
    var i: i32 = 0;
    while (i < 5) {
        ma = ma.insert("k" + i.to_string(), Xa { s: "x" + i.to_string(), n: i });
        mb = mb.insert("k" + (i % 2).to_string(), Yb { t: "t" + i.to_string(), u: "u" + i.to_string() });
        i = i + 1;
    }
    var r: (Map[string, dyn Bb], boolean) = mb.without("k0");
    mb = r.0;
    var t: i32 = 0;
    for (k, v) in ma { t = t + v.a(); }
    for (k, v) in mb { t = t + v.b(); }
    var xs: dyn Bb[] = [Yb { t: "p" + "q", u: "r" }];
    t = t + xs[0].b();
    print(t.to_string());
    return 0;
}
`

// routedMapGenericDynValuesSrc: a dyn over a generic trait pinned at two
// arguments names its release by a trait set that is spelled with a comma,
// brackets and a space, none of which a symbol may hold.
const routedMapGenericDynValuesSrc = `import "core/map";
import "std/i32";
trait Tagged[A, B] { function tag(self: Self): i32; }
struct P { a: i32, s: string }
struct Q { t: string }
impl Tagged[i32, u32] for P { function tag(self: Self): i32 { return self.a + self.s.len(); } }
impl Tagged[i32, u32] for Q { function tag(self: Self): i32 { return self.t.len() * 10; } }
function main(): i32 {
    var m: Map[string, dyn Tagged[i32, u32]] = Map {};
    var i: i32 = 0;
    while (i < 5) {
        if (i % 2 == 0) { m = m.insert("k" + (i % 3).to_string(), P { a: i, s: "p" + i.to_string() }); }
        else { m = m.insert("k" + (i % 3).to_string(), Q { t: "q" + i.to_string() }); }
        i = i + 1;
    }
    var r: (Map[string, dyn Tagged[i32, u32]], boolean) = m.without("k0");
    var t: i32 = 0;
    for (k, v) in r.0 { t = t + v.tag(); }
    for v in m.values() { t = t + v.tag(); }
    var ds: dyn Tagged[i32, u32][] = [Q { t: "x" + "y" }];
    var flags: boolean[] = [true, false];
    t = t + ds[0].tag() + flags.len();
    print(t.to_string());
    return 0;
}
`

// routedMapStringArrayValuesSrc: a string[] value is a box whose release also
// releases every string it holds.
const routedMapStringArrayValuesSrc = `import "core/map";
import "std/i32";
function main(): i32 {
    var m: Map[string, string[]] = Map {};
    var i: i32 = 0;
    while (i < 40) { m = m.insert("k" + (i % 9).to_string(), ["a" + i.to_string(), "b", "c" + (i * 2).to_string()]); i = i + 1; }
    var alias: Map[string, string[]] = m;
    alias = alias.insert("k1", ["over" + "ride"]);
    var r: (Map[string, string[]], boolean) = m.without("k2");
    var m2: Map[string, string[]] = r.0;
    var total: i32 = 0;
    for (k, v) in m2 { total = total + v.len() + v[0].len(); }
    for v in alias.values() { total = total + v[v.len() - 1].len(); }
    var hit: string[] = m.get_or("k3", []);
    var miss: string[] = m.get_or("zz", ["x" + "y"]);
    var got: string = "none";
    match (alias.get("k1")) { Some(v) => { got = v[0]; }, None => {} }
    print(total.to_string() + " " + hit[0] + " " + miss[0] + " " + got + " " + m2.len().to_string() + " " + r.1.to_string());
    return 0;
}
`

func TestSelfHostRoutedScalarMaps(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	selfHostBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	cases := []struct{ name, src, want string }{
		{"surface", routedMapSurfaceSrc, "104398092"},
		{"negative_keys", routedMapNegativeKeysSrc, "-150 -147 2847 false"},
		{"cow", routedMapCowSrc, "14 1000 true false 100 100 99"},
		{"u32_bool", routedMapU32BoolSrc, "true true 2 12"},
		{"string_values", routedMapStringValuesSrc, "overwritten aliasonly v24 missed v27 true true false 2254 299 n44 9 0"},
		{"box_values", routedMapBoxValuesSrc, "2 5 3 4 27 true true false 21236 199 6 four! n44 9 12 21627 0"},
		{"tuple_values", routedMapTupleValuesSrc, "430 v24 override 6 true"},
		{"dyn_values", routedMapDynValuesSrc, "716"},
		{"two_dyn_values", routedMapTwoDynValuesSrc, "27"},
		{"generic_dyn_values", routedMapGenericDynValuesSrc, "62"},
		{"string_array_values", routedMapStringArrayValuesSrc, "80 a39 xy override 8 true"},
		{"string_keys", routedMapStringKeysSrc, "202311 691 61358 0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stdout, stderr := routedMapRun(t, selfHostBin, stdlibRoot, c.src, target, "FERN_SANITIZE=1")
					if stdout != c.want {
						t.Fatalf("stdout = %q, want %q\n%s", stdout, c.want, stderr)
					}
					if target == "x86-64-linux" && !strings.Contains(stderr, "leakcheck:") {
						t.Fatalf("no leak census line\n%s", stderr)
					}
					if strings.Contains(stderr, "fern-sanitizer:") || (strings.Contains(stderr, "leakcheck:") && !strings.Contains(stderr, "live_bytes=0")) {
						t.Fatalf("heap finding:\n%s", stderr)
					}
				})
			}
		})
	}

	// Each routed program calls core/map and nothing of the runtime's map, so
	// a value column that stopped routing turns its case red here rather than
	// passing on the runtime map.
	for _, c := range cases {
		asm := routedMapAsm(t, selfHostBin, stdlibRoot, c.src)
		if !strings.Contains(asm, "call __fn___map_set_impl") || strings.Contains(asm, "call __fern_map_set") {
			t.Fatalf("the %s program's maps are not routed onto core/map", c.name)
		}
	}

	// A bisect knob keeps a mixed module, so under one every map stays on the
	// runtime and the program still runs.
	t.Run("bisect_knob", func(t *testing.T) {
		asm := routedMapAsm(t, selfHostBin, stdlibRoot, routedMapSurfaceSrc, "FERN_SEM_IR_SKIP=__no_such_function__")
		if strings.Contains(asm, "call __fn___map_set_impl") {
			t.Fatal("a map was routed under FERN_SEM_IR_SKIP")
		}
		stdout, stderr := routedMapRun(t, selfHostBin, stdlibRoot, routedMapSurfaceSrc, "x86-64-linux", "FERN_SEM_IR_SKIP=__no_such_function__")
		if stdout != "104398092" {
			t.Fatalf("stdout = %q, want 104398092\n%s", stdout, stderr)
		}
	})
}

func routedMapAsm(t *testing.T, fernBin, stdlibRoot, src string, env ...string) string {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog.s")
	cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-emit", "asm", in, stdlibRoot, "-o", out)
	cmd.Env = append(os.Environ(), env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, msg)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// routedMapRun compiles src for target with the typed lowering held to
// production (FERN_SEM_IR_STRICT), runs it and returns trimmed stdout and
// stderr. `env` joins the compile's environment.
func routedMapRun(t *testing.T, fernBin, stdlibRoot, src, target string, env ...string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	args := []string{"-target", target, in, stdlibRoot, "-o", out}
	if target == "wasm32-wasi" {
		out = filepath.Join(dir, "prog.wat")
		args = []string{"-target", target, "-emit", "asm", in, stdlibRoot, "-o", out}
	}
	cmd := exec.Command(fernBin, args...)
	cmd.Env = append(append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SEM_IR_STRICT=1"), env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile (%s): %v\n%s", target, err, msg)
	}
	var run *exec.Cmd
	switch target {
	case "x86-64-linux":
		_, runner := x86_64Tooling(t)
		run = runX86_64Bin(runner, out)
	case "arm64-linux":
		_, qemu := arm64Tooling(t)
		run = runArm64Bin(qemu, out)
	default:
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Fatal("wasmtime not on PATH")
		}
		run = exec.Command("wasmtime", "run", "--dir", dir+"::.", out)
	}
	var stdout, stderr strings.Builder
	run.Stdout = &stdout
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("run (%s): %v\n%s", target, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), stderr.String()
}
