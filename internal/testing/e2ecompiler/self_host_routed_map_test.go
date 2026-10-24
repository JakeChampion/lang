package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const routedMapSurfaceSrc = `import "core/map";
import "std/i32";

struct Holder { m: Map[i32, i32], tag: i32 }

function build(n: i32): Map[i32, i32] {
    let m: Map[i32, i32] = Map {};
    let i: i32 = 0;
    while (i < n) { m = m.insert(i + 50, (i + 50) * 3); i = i + 1; }
    return m;
}

function sum_iter(m: Map[i32, i32]): i32 {
    let s: i32 = 0;
    for (k, v) in m { s = s + k * 7 + v; }
    return s;
}

function main(): i32 {
    let m: Map[i32, i32] = build(1000);
    let alias: Map[i32, i32] = m;
    alias = alias.insert(5000, 1);
    let s: i32 = m.len() * 100000 + alias.len();
    s = s + m.get_or(50, 0) + m.get_or(123456, -9);
    match (m.get(51)) { Some(v) => { s = s + v; }, None => { s = s - 1; } }
    match (m.get(99999)) { Some(v) => { s = s + v; }, None => { s = s - 1; } }
    let r_had: boolean = m.contains(50);
    let r: Map[i32, i32] = m.remove(50);
    let m2: Map[i32, i32] = r;
    if (r_had) { s = s + 11; }
    if (m.contains(50) && !m2.contains(50)) { s = s + 13; }
    s = s + sum_iter(m2);
    let ks: i32[] = m2.keys();
    let vs: i32[] = m2.values();
    let ki: i32 = 0;
    while (ki < ks.len()) { s = s + ks[ki] - vs[ki]; ki = ki + 1; }
    let h: Holder = Holder { m: build(10), tag: 4 };
    s = s + h.m.len() + h.tag;
    let hs: Holder[] = [h, Holder { m: m2, tag: 1 }];
    s = s + hs[1].m.len();
    let cap: Map[i32, i32] = build(5);
    let f = (x: i32): i32 => { return cap.get_or(x, 0) + cap.len(); };
    s = s + f(52);
    let e: Map[i32, i32] = m.cleared();
    s = s + e.len();
    for (k, v) in e { s = s + k + v; }
    print(s.to_string());
    return 0;
}
`

const routedMapNegativeKeysSrc = `import "core/map";
import "std/i32";
function main(): i32 {
    let m: Map[i32, i32] = Map {};
    let i: i32 = 0;
    while (i < 1000) { m = m.insert(i - 50, (i - 50) * 3); i = i + 1; }
    let got: string = "none";
    match (m.get(-49)) { Some(v) => { got = v.to_string(); }, None => {} }
    print(m.get_or(-50, 7777).to_string() + " " + got + " " + m.get_or(949, 0).to_string() + " " + m.contains(-51).to_string());
    return 0;
}
`

const routedMapCowSrc = `import "core/map";
import "std/i32";
function build(n: i32): Map[i32, i32] {
    let m: Map[i32, i32] = Map {};
    let i: i32 = 0;
    while (i < n) { m = m.insert(i, i * 2); i = i + 1; }
    return m;
}
function main(): i32 {
    let a: Map[i32, i32] = build(100);
    let b: Map[i32, i32] = a;
    b = b.insert(7, 1000);
    let c: Map[i32, i32] = a;
    let r: Map[i32, i32] = c.remove(3);
    c = r;
    print(a.get_or(7, 0).to_string() + " " + b.get_or(7, 0).to_string() + " " + a.contains(3).to_string() + " "
        + c.contains(3).to_string() + " " + a.len().to_string() + " " + b.len().to_string() + " " + c.len().to_string());
    return 0;
}
`

const routedMapU32BoolSrc = `import "core/map";
import "std/i32";
function main(): i32 {
    let big: Map[u32, u32] = Map {};
    big = big.insert(4000000000 as u32, 3000000000 as u32);
    big = big.insert(1 as u32, 2 as u32);
    let sum: u64 = 0 as u64;
    for (k, v) in big { sum = sum + (k as u64) + (v as u64); }
    let flags: Map[boolean, boolean] = Map {};
    flags = flags.insert(true, false);
    flags = flags.insert(false, true);
    flags = flags.insert(true, true);
    let trues: i32 = 0;
    for v in flags.values() { if (v) { trues = trues + 1; } }
    for k in flags.keys() { if (k) { trues = trues + 10; } }
    print((big.get_or(4000000000 as u32, 0 as u32) == (3000000000 as u32)).to_string() + " "
        + (sum == (7000000003 as u64)).to_string() + " " + flags.len().to_string() + " " + trues.to_string());
    return 0;
}
`

const routedMapStringKeysSrc = `import "core/map";
import "std/i32";
function key(i: i32): string { return "k" + i.to_string(); }
function build(n: i32): Map[string, i32] {
    let m: Map[string, i32] = Map {};
    let i: i32 = 0;
    while (i < n) { m = m.insert(key(i), i * 3); i = i + 1; }
    return m;
}
function main(): i32 {
    let m: Map[string, i32] = build(200);
    m = m.insert(key(5), 999);
    m = m.insert("lit", 7);
    let alias: Map[string, i32] = m;
    alias = alias.insert(key(1000), 1);
    alias = alias.insert(key(6), 66);
    let s: i32 = m.len() * 1000 + alias.len();
    s = s + m.get_or(key(5), 0) + m.get_or("nope", -9) + alias.get_or(key(6), 0) + m.get_or(key(6), 0);
    if (m.contains("lit")) { s = s + 1; }
    match (m.get(key(7))) { Some(v) => { s = s + v; }, None => { s = s - 1; } }
    let r_had: boolean = m.contains(key(8));
    let r: Map[string, i32] = m.remove(key(8));
    let m2: Map[string, i32] = r;
    if (r_had && !m2.contains(key(8)) && m.contains(key(8))) { s = s + 13; }
    let r2: Map[string, i32] = m2.remove("absent");
    m2 = r2;
    let kl: i32 = 0;
    for k in m2.keys() { kl = kl + k.len(); }
    let it: i32 = 0;
    for (k, v) in m2 { it = it + k.len() + v; }
    let e: Map[string, i32] = m2.cleared();
    print(s.to_string() + " " + kl.to_string() + " " + it.to_string() + " " + e.len().to_string());
    return 0;
}
`

const routedMapKeyedKeysSrc = `import "core/map";
import "core/cmp";
import "std/i32";
import "std/i64";
@derive(cmp.Eq, cmp.Hash)
struct Pt { x: i32, tag: string }
@derive(cmp.Eq, cmp.Hash)
enum Shape { Dot, Sq(i32), Named(string) }
struct Rec { name: string, n: i32 }
struct Holder { idx: Map[Pt, string] }
function pt(i: i32): Pt { return Pt { x: i, tag: "t" + (i % 5).to_string() }; }
function main(): i32 {
    let a: Map[Pt, i32] = map_new(4);
    let i: i32 = 0;
    while (i < 60) { a = a.insert(pt(i), i * 2); i = i + 1; }
    a = a.insert(pt(7), 700);
    let alias: Map[Pt, i32] = a;
    a = a.insert(pt(8), 800);
    let had: boolean = a.contains(pt(9));
    let a2 = a.remove(pt(9));
    let s: i32 = a2.get_or(pt(7), 0) + a2.get_or(pt(8), 0) + alias.get_or(pt(8), 0) + a2.get_or(pt(9), -1) + a2.len() + alias.len();
    if (a2.contains(pt(10)) && !a2.contains(pt(9)) && had) { s = s + 1; }
    let b: Map[Shape, string] = Map { Dot: "dot", Sq(2): "sq2" };
    b = b.insert(Named("n" + "1"), "named");
    b = b.insert(Sq(2), "two");
    let bs: string = b.get_or(Sq(2), "-") + b.get_or(Named("n1"), "-") + b.get_or(Sq(3), "-");
    match (b.get(Dot)) { Some(v) => { bs = bs + v; }, None => { bs = bs + "?"; } }
    let c: Map[(string, i32), Rec] = map_new(4);
    i = 0;
    while (i < 20) { c = c.insert(("r" + i.to_string(), i % 3), Rec { name: "n" + i.to_string(), n: i }); i = i + 1; }
    c = c.insert(("r4", 1), Rec { name: "four" + "!", n: 4 });
    let gone: boolean = c.contains(("r5", 2));
    let c2 = c.remove(("r5", 2));
    let cs: string = c2.get_or(("r4", 1), Rec { name: "", n: 0 }).name + " " + c2.len().to_string();
    let d: Map[i32[], i32] = Map { [1, 2]: 3 };
    d = d.insert([4], 5);
    let kl: i32 = 0;
    for k in a2.keys() { kl = kl + k.x; }
    let it: i32 = 0;
    for (k, v) in c2 { it = it + k.1 + v.n; }
    let h: Holder = Holder { idx: Map { pt(1): "one" } };
    h = Holder { idx: h.idx.insert(pt(2), "two") };
    let w: Map[Pt, i64] = map_new(4);
    i = 0;
    while (i < 30) { w = w.insert(pt(i), (i as i64) * 5000000000i64); i = i + 1; }
    w = w.insert(pt(3), 1i64);
    let walias: Map[Pt, i64] = w;
    w = w.insert(pt(4), 2i64);
    let wgone: boolean = w.contains(pt(5));
    let w2 = w.remove(pt(5));
    let wsum: i64 = w2.get_or(pt(3), 0i64) + w2.get_or(pt(4), 0i64) + walias.get_or(pt(4), 0i64) + w2.get_or(pt(5), 7i64);
    match (w2.get(pt(6))) { Some(v) => { wsum = wsum + v; }, None => {} }
    let fl: Map[(i32, i32), f64] = Map { (1, 2): 1.5 };
    fl = fl.insert((3, 4), 2.25);
    fl = fl.insert((1, 2), 0.5);
    let fs: f64 = fl.get_or((1, 2), 0.0) + fl.get_or((3, 4), 0.0) + fl.get_or((9, 9), 10.0);
    match (fl.get((3, 4))) { Some(v) => { fs = fs + v; }, None => {} }
    let e: Map[Pt, i32] = a2.cleared();
    print(s.to_string() + " " + bs + " " + cs + " " + d.get_or([1, 2], 0).to_string() + " " + kl.to_string() + " " + it.to_string() + " " + h.idx.get_or(pt(2), "") + " " + e.len().to_string() + " " + wsum.to_string() + " " + ((fs * 100.0) as i32).to_string());
    return 0;
}
`

const routedMapGenericKeyedKeysSrc = `import "core/map";
import "core/cmp";
import "std/i32";
@derive(cmp.Eq, cmp.Hash)
struct Pair[T] { a: T, b: T }
@derive(cmp.Eq, cmp.Hash)
enum Slot[T] { Empty, One(T), Two(T, T) }
function main(): i32 {
    let p: Map[Pair[string], i32] = map_new(4);
    let i: i32 = 0;
    while (i < 40) { p = p.insert(Pair[string] { a: "a" + i.to_string(), b: "b" + (i % 7).to_string() }, i); i = i + 1; }
    p = p.insert(Pair[string] { a: "a3", b: "b3" }, 300);
    let palias: Map[Pair[string], i32] = p;
    p = p.insert(Pair[string] { a: "a4", b: "b4" }, 400);
    let phad: boolean = p.contains(Pair[string] { a: "a5", b: "b5" });
    let p2 = p.remove(Pair[string] { a: "a5", b: "b5" });
    let s: i32 = p2.get_or(Pair[string] { a: "a3", b: "b3" }, 0) + p2.get_or(Pair[string] { a: "a4", b: "b4" }, 0) + palias.get_or(Pair[string] { a: "a4", b: "b4" }, 0) + p2.len() + palias.len();
    if (phad && !p2.contains(Pair[string] { a: "a5", b: "b5" })) { s = s + 1; }
    let q: Map[Slot[i32], string] = map_new(4);
    i = 0;
    while (i < 30) { q = q.insert(Slot.Two(i, i * 3), "t" + i.to_string()); i = i + 1; }
    q = q.insert(Slot.Empty, "empty");
    q = q.insert(Slot.One(5), "five");
    q = q.insert(Slot.Two(2, 6), "two");
    let qs: string = q.get_or(Slot.Two(2, 6), "-") + q.get_or(Slot.Empty, "-") + q.get_or(Slot.One(6), "-");
    match (q.get(Slot.One(5))) { Some(v) => { qs = qs + v; }, None => { qs = qs + "?"; } }
    let qhad: boolean = q.contains(Slot.Empty);
    let q2 = q.remove(Slot.Empty);
    if (qhad && !q2.contains(Slot.Empty) && q.contains(Slot.Empty)) { qs = qs + "!"; }
    let ks: i32 = 0;
    for k in q2.keys() {
        match (k) { Empty => { ks = ks + 1000; }, One(x) => { ks = ks + x; }, Two(x, y) => { ks = ks + x + y; } }
    }
    print(s.to_string() + " " + qs + " " + q2.len().to_string() + " " + ks.to_string());
    return 0;
}
`

const routedMapMapValuesSrc = `import "core/map";
import "std/i32";
function build(n: i32): Map[string, Map[i32, i32]] {
  let outer: Map[string, Map[i32, i32]] = map_new(2);
  let i: i32 = 0;
  while (i < n) {
    let inner: Map[i32, i32] = map_new(2);
    let j: i32 = 0;
    while (j < 5) { inner = inner.insert(j, i * 10 + j); j = j + 1; }
    outer = outer.insert("k" + i.to_string(), inner);
    i = i + 1;
  }
  return outer;
}
function main(): i32 {
  let outer: Map[string, Map[i32, i32]] = build(40);
  let alias: Map[string, Map[i32, i32]] = outer;
  let e: Map[i32, i32] = map_new(1);
  let got: Map[i32, i32] = outer.get_or("k3", e);
  got = got.insert(99, 7);
  let s: i32 = outer.get_or("k3", e).len() * 1000 + got.len();
  outer = outer.insert("k3", got);
  s = s + outer.get_or("k3", e).get_or(99, 0) * 100 + alias.get_or("k3", e).get_or(99, -1);
  let had: boolean = outer.contains("k4");
  let o2 = outer.remove("k4");
  if (had && !o2.contains("k4") && outer.contains("k4")) { s = s + 1; }
  let t: i32 = 0;
  for (k, v) in o2 { t = t + v.len() + k.len(); }
  for v in o2.values() { t = t + v.get_or(0, 0); }
  match (o2.get("k7")) { Some(m) => { t = t + m.get_or(2, 0); }, None => {} }
  let cleared: Map[string, Map[i32, i32]] = o2.cleared();
  print(s.to_string() + " " + t.to_string() + " " + cleared.len().to_string());
  return 0;
}
`

const routedMapViewValuesSrc = `import "core/map";
import "core/cmp";
import "std/i32";
@derive(cmp.Eq, cmp.Hash)
struct Pt { x: i32, y: i32 }
struct Holder { m: Map[i32, str], tag: i32 }
function fill(s: string, n: i32): Map[i32, str] {
    let m: Map[i32, str] = Map {};
    let i: i32 = 0;
    while (i < n) { m = m.insert(i, slice_unchecked(s, i % 5, i % 5 + 3)); i = i + 1; }
    m = m.insert(100, "lit");
    m = m.insert(101, slice_unchecked("literal", 1, 4));
    return m;
}
function grow(m: Map[i32, str], s: string): Map[i32, str] {
    return m.insert(200, slice_unchecked(s, 0, 1));
}
function main(): i32 {
    let s: string = "abcdefgh" + 7.to_string();
    let t: string = s + "!";
    let c: str = t;
    let m: Map[i32, str] = fill(s, 40);
    m = m.insert(102, c);
    let alias: Map[i32, str] = m;
    alias = alias.insert(0, "over");
    alias = alias.insert(1, slice_unchecked(t, 6, 10));
    let r_had: boolean = m.contains(2);
    let r: Map[i32, str] = m.remove(2);
    let m2: Map[i32, str] = r;
    m = m.insert(3, slice_unchecked(s, 7, 9));
    let m3: Map[i32, str] = grow(m, s);
    let junk: string[] = [];
    let j: i32 = 0;
    while (j < 64) { junk = junk.append("zzzzzzzzz" + j.to_string()); j = j + 1; }
    let out: string = m.get_or(0, "q") + "," + alias.get_or(0, "q") + "," + alias.get_or(1, "q") + "," + m.get_or(3, "q")
        + "," + m.get_or(100, "q") + "," + m.get_or(101, "q") + "," + m.get_or(102, "q") + "," + m.get_or(999, "miss")
        + "," + m2.get_or(2, "gone") + "," + m.get_or(2, "q") + "," + m3.get_or(200, "q") + "," + r_had.to_string();
    match (m.get(102)) { Some(v) => { out = out + "," + v; }, None => { out = out + ",none"; } }
    match (m.get(998)) { Some(v) => { out = out + "," + v; }, None => { out = out + ",none"; } }
    let total: i32 = 0;
    for (k, v) in m2 { total = total + k + v.len(); }
    for v in alias.values() { total = total + v.len(); }
    for k in m3.keys() { total = total + k; }
    let h: Holder = Holder { m: fill(s, 4), tag: 2 };
    let hs: Holder[] = [h, Holder { m: m2, tag: 1 }];
    total = total + hs[0].m.len() + hs[1].m.len() + h.tag;
    let e: Map[i32, str] = m.cleared();
    total = total + e.len();
    let named: Map[str, str] = Map {};
    named = named.insert(slice_unchecked(s, 0, 2), slice_unchecked(s, 2, 4));
    named = named.insert("k", c);
    named = named.insert(slice_unchecked(s, 0, 2), "again");
    let wide: Map[i64, str] = Map {};
    wide = wide.insert(5 as i64, slice_unchecked(t, 1, 3));
    wide = wide.insert(5 as i64, slice_unchecked(t, 2, 5));
    let pts: Map[Pt, str] = Map {};
    pts = pts.insert(Pt { x: 1, y: 2 }, slice_unchecked(s, 3, 6));
    pts = pts.insert(Pt { x: 1, y: 2 }, "pt");
    pts = pts.insert(Pt { x: 3, y: 4 }, c);
    let pts2: Map[Pt, str] = pts;
    let pr: Map[Pt, str] = pts2.remove(Pt { x: 3, y: 4 });
    out = out + "," + named.get_or("ab", "q") + "," + named.get_or("k", "q") + "," + wide.get_or(5 as i64, "q")
        + "," + pts.get_or(Pt { x: 1, y: 2 }, "q") + "," + pts.get_or(Pt { x: 3, y: 4 }, "q") + "," + pr.len().to_string();
    print(out);
    print((total + junk.len()).to_string());
    return 0;
}
`

const routedMapUsizeValuesSrc = `import "core/map";
import "std/i32";
function main(): i32 {
    let big: usize = (3000000000 as usize) * (4 as usize);
    let m: Map[i32, usize] = Map {};
    let i: i32 = 0;
    while (i < 300) { m = m.insert(i, big + (i as usize)); i = i + 1; }
    m = m.insert(7, big + (1000 as usize));
    let alias: Map[i32, usize] = m;
    alias = alias.insert(8, 5 as usize);
    let r: Map[i32, usize] = m.remove(9);
    let hit: boolean = m.get_or(7, 0 as usize) == big + (1000 as usize) && alias.get_or(8, 0 as usize) == (5 as usize)
        && m.get_or(8, 0 as usize) == big + (8 as usize) && m.get_or(999, 3 as usize) == (3 as usize) && !r.contains(9) && m.contains(9);
    let got: i32 = 0;
    match (m.get(299)) { Some(v) => { if (v == big + (299 as usize)) { got = 1; } }, None => {} }
    match (m.get(300)) { Some(v) => { got = 100; }, None => { got = got + 2; } }
    let n: i32 = 0;
    for (k, v) in r { if (v - big == (k as usize) || k == 7) { n = n + 1; } }
    for v in alias.values() { if (v >= big) { n = n + 1; } }
    let named: Map[string, usize] = Map {};
    named = named.insert("a" + 1.to_string(), big);
    named = named.insert("a1", big + (2 as usize));
    let e: Map[i32, usize] = m.cleared();
    print(hit.to_string() + " " + got.to_string() + " " + n.to_string() + " " + ((named.get_or("a1", 0 as usize) - big) as i32).to_string()
        + " " + e.len().to_string() + " " + r.len().to_string());
    return 0;
}
`

const routedMapUsizeKeysSrc = `import "core/map";
import "std/i32";
function main(): i32 {
    let big: usize = (3000000000 as usize) * (4 as usize);
    let m: Map[usize, i32] = Map {};
    let i: i32 = 0;
    while (i < 300) { m = m.insert(big + (i as usize), i * 2); i = i + 1; }
    m = m.insert(big + (7 as usize), 1000);
    let r_had: boolean = m.contains(big + (9 as usize));
    let r: Map[usize, i32] = m.remove(big + (9 as usize));
    let s: i32 = m.get_or(big + (7 as usize), 0) + m.get_or(5 as usize, -1) + r.len();
    match (m.get(big + (299 as usize))) { Some(v) => { s = s + v; }, None => { s = s - 1; } }
    let n: i32 = 0;
    for (k, v) in r { if (k >= big) { n = n + 1; } }
    let ks: usize[] = m.keys();
    print(s.to_string() + " " + n.to_string() + " " + ks.len().to_string() + " " + r_had.to_string());
    return 0;
}
`

const routedMapCharValuesSrc = `import "core/map";
import "std/i32";
function main(): i32 {
    let m: Map[i32, char] = Map {};
    m = m.insert(1, 'a');
    m = m.insert(2, 'β');
    m = m.insert(1, 'z');
    let s: string = "";
    match (m.get(1)) { Some(c) => { s = s + (c as i32).to_string(); }, None => {} }
    s = s + (m.get_or(2, '?') as i32).to_string() + "," + (m.get_or(3, '?') as i32).to_string();
    let n: i32 = 0;
    for c in m.values() { n = n + (c as i32); }
    print(s + " " + n.to_string() + " " + m.contains(2).to_string());
    return 0;
}
`

const routedMapStringValuesSrc = `import "core/map";
import "std/i32";

function build(n: i32): Map[string, string] {
    let m: Map[string, string] = Map {};
    let i: i32 = 0;
    while (i < n) { m = m.insert("k" + i.to_string(), "v" + (i * 3).to_string()); i = i + 1; }
    return m;
}

function main(): i32 {
    let m: Map[string, string] = build(300);
    m = m.insert("k7", "over" + "written");
    let alias: Map[string, string] = m;
    alias = alias.insert("k8", "alias" + "only");
    let out: string = m.get_or("k7", "none") + " " + alias.get_or("k8", "none") + " " + m.get_or("k8", "none");
    out = out + " " + m.get_or("zz", "miss" + "ed");
    match (m.get("k9")) { Some(v) => { out = out + " " + v; }, None => { out = out + " none"; } }
    let r_had: boolean = m.contains("k10");
    let r: Map[string, string] = m.remove("k10");
    let m2: Map[string, string] = r;
    out = out + " " + r_had.to_string() + " " + m.contains("k10").to_string() + " " + m2.contains("k10").to_string();
    let total: i32 = 0;
    for (k, v) in m2 { total = total + k.len() + v.len(); }
    let vs: string[] = m2.values();
    out = out + " " + total.to_string() + " " + vs.len().to_string();
    let by_id: Map[i32, string] = Map {};
    let j: i32 = 0;
    while (j < 50) { by_id = by_id.insert(j % 10, "n" + j.to_string()); j = j + 1; }
    let r2: Map[i32, string] = by_id.remove(3);
    by_id = r2;
    out = out + " " + by_id.get_or(4, "") + " " + by_id.len().to_string();
    let e: Map[string, string] = m.cleared();
    out = out + " " + e.len().to_string();
    print(out);
    return 0;
}
`

const routedMapBoxValuesSrc = `import "core/map";
import "std/i32";

struct Rec { name: string, n: i32 }
enum Shape { Circle(i32), Label(string), Empty }

function build(n: i32): Map[string, i32[]] {
    let m: Map[string, i32[]] = Map {};
    let i: i32 = 0;
    while (i < n) { m = m.insert("k" + i.to_string(), [i, i * 2, i * 3]); i = i + 1; }
    return m;
}

function shape_n(s: Shape): i32 {
    match (s) { Circle(r) => { return r; }, Label(t) => { return t.len(); }, Empty => { return 0; } }
}

function main(): i32 {
    let m: Map[string, i32[]] = build(200);
    m = m.insert("k7", [70, 71]);
    let alias: Map[string, i32[]] = m;
    alias = alias.insert("k8", [1, 2, 3, 4, 5]);
    let out: string = m.get_or("k7", []).len().to_string() + " " + alias.get_or("k8", []).len().to_string() + " " + m.get_or("k8", []).len().to_string();
    out = out + " " + m.get_or("zz", [9, 9, 9, 9]).len().to_string();
    match (m.get("k9")) { Some(v) => { out = out + " " + v[2].to_string(); }, None => { out = out + " none"; } }
    let r_had: boolean = m.contains("k10");
    let r: Map[string, i32[]] = m.remove("k10");
    let m2: Map[string, i32[]] = r;
    out = out + " " + r_had.to_string() + " " + m.contains("k10").to_string() + " " + m2.contains("k10").to_string();
    let total: i32 = 0;
    for (k, v) in m2 { total = total + k.len() + v.len() + v[0]; }
    let vs: i32[][] = m2.values();
    out = out + " " + total.to_string() + " " + vs.len().to_string() + " " + vs[3][1].to_string();

    let recs: Map[i32, Rec] = Map {};
    let j: i32 = 0;
    while (j < 50) { recs = recs.insert(j % 10, Rec { name: "n" + j.to_string(), n: j }); j = j + 1; }
    let r2: Map[i32, Rec] = recs.remove(3);
    recs = r2;
    let hold: Map[i32, Rec] = recs;
    recs = recs.insert(4, Rec { name: "four" + "!", n: 4 });
    out = out + " " + recs.get_or(4, Rec { name: "", n: 0 }).name + " " + hold.get_or(4, Rec { name: "", n: 0 }).name + " " + recs.len().to_string();
    for (k, v) in recs { total = total + v.n + v.name.len(); }

    let shapes: Map[string, Shape] = Map {};
    shapes = shapes.insert("a", Circle(3));
    shapes = shapes.insert("b", Label("hello" + "!"));
    shapes = shapes.insert("c", Empty);
    shapes = shapes.insert("b", Label("bye"));
    let sn: i32 = 0;
    for (k, v) in shapes { sn = sn + shape_n(v); }
    for v in shapes.values() { sn = sn + shape_n(v); }
    out = out + " " + sn.to_string() + " " + total.to_string();
    let e: Map[string, i32[]] = m.cleared();
    out = out + " " + e.len().to_string();
    print(out);
    return 0;
}
`

const routedMapTupleValuesSrc = `import "core/map";
import "std/i32";
function main(): i32 {
    let m: Map[string, (i32, string)] = Map {};
    let i: i32 = 0;
    while (i < 30) { m = m.insert("k" + (i % 7).to_string(), (i, "v" + i.to_string())); i = i + 1; }
    let alias: Map[string, (i32, string)] = m;
    alias = alias.insert("k1", (100, "over" + "ride"));
    let r_had: boolean = m.contains("k2");
    let r: Map[string, (i32, string)] = m.remove("k2");
    let m2: Map[string, (i32, string)] = r;
    let total: i32 = 0;
    for (k, v) in m2 { total = total + v.0 + v.1.len(); }
    for v in alias.values() { total = total + v.0; }
    let hit: (i32, string) = m.get_or("k3", (0, ""));
    let got: string = "none";
    match (alias.get("k1")) { Some(v) => { got = v.1; }, None => {} }
    print(total.to_string() + " " + hit.1 + " " + got + " " + m2.len().to_string() + " " + r_had.to_string());
    return 0;
}
`

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
    let m: Map[string, dyn Show] = Map {};
    m = m.insert("a", 41);
    m = m.insert("b", "hello" + "!");
    m = m.insert("c", Dot { r: 5 });
    m = m.insert("d", Add(7));
    let alias: Map[string, dyn Show] = m;
    alias = alias.insert("b", Neg);
    let r: Map[string, dyn Show] = m.remove("c");
    let total: i32 = 0;
    for (k, v) in r { total = total + v.show(); }
    for v in alias.values() { total = total + v.show(); }
    match (m.get("b")) { Some(v) => { total = total + v.show() * 100; }, None => {} }
    print(total.to_string());
    return 0;
}
`

const routedMapTwoDynValuesSrc = `import "core/map";
import "std/i32";
trait Aa { function a(self: Self): i32; }
trait Bb { function b(self: Self): i32; }
struct Xa { s: string, n: i32 }
struct Yb { t: string, u: string }
impl Aa for Xa { function a(self: Self): i32 { return self.s.len() + self.n; } }
impl Bb for Yb { function b(self: Self): i32 { return self.t.len() + self.u.len(); } }
function main(): i32 {
    let ma: Map[string, dyn Aa] = Map {};
    let mb: Map[string, dyn Bb] = Map {};
    let i: i32 = 0;
    while (i < 5) {
        ma = ma.insert("k" + i.to_string(), Xa { s: "x" + i.to_string(), n: i });
        mb = mb.insert("k" + (i % 2).to_string(), Yb { t: "t" + i.to_string(), u: "u" + i.to_string() });
        i = i + 1;
    }
    let r: Map[string, dyn Bb] = mb.remove("k0");
    mb = r;
    let t: i32 = 0;
    for (k, v) in ma { t = t + v.a(); }
    for (k, v) in mb { t = t + v.b(); }
    let xs: dyn Bb[] = [Yb { t: "p" + "q", u: "r" }];
    t = t + xs[0].b();
    print(t.to_string());
    return 0;
}
`

const routedMapGenericDynValuesSrc = `import "core/map";
import "std/i32";
trait Tagged[A, B] { function tag(self: Self): i32; }
struct P { a: i32, s: string }
struct Q { t: string }
impl Tagged[i32, u32] for P { function tag(self: Self): i32 { return self.a + self.s.len(); } }
impl Tagged[i32, u32] for Q { function tag(self: Self): i32 { return self.t.len() * 10; } }
function main(): i32 {
    let m: Map[string, dyn Tagged[i32, u32]] = Map {};
    let i: i32 = 0;
    while (i < 5) {
        if (i % 2 == 0) { m = m.insert("k" + (i % 3).to_string(), P { a: i, s: "p" + i.to_string() }); }
        else { m = m.insert("k" + (i % 3).to_string(), Q { t: "q" + i.to_string() }); }
        i = i + 1;
    }
    let r: Map[string, dyn Tagged[i32, u32]] = m.remove("k0");
    let t: i32 = 0;
    for (k, v) in r { t = t + v.tag(); }
    for v in m.values() { t = t + v.tag(); }
    let ds: dyn Tagged[i32, u32][] = [Q { t: "x" + "y" }];
    let flags: boolean[] = [true, false];
    t = t + ds[0].tag() + flags.len();
    print(t.to_string());
    return 0;
}
`

const routedMapStringArrayValuesSrc = `import "core/map";
import "std/i32";
function main(): i32 {
    let m: Map[string, string[]] = Map {};
    let i: i32 = 0;
    while (i < 40) { m = m.insert("k" + (i % 9).to_string(), ["a" + i.to_string(), "b", "c" + (i * 2).to_string()]); i = i + 1; }
    let alias: Map[string, string[]] = m;
    alias = alias.insert("k1", ["over" + "ride"]);
    let r_had: boolean = m.contains("k2");
    let r: Map[string, string[]] = m.remove("k2");
    let m2: Map[string, string[]] = r;
    let total: i32 = 0;
    for (k, v) in m2 { total = total + v.len() + v[0].len(); }
    for v in alias.values() { total = total + v[v.len() - 1].len(); }
    let hit: string[] = m.get_or("k3", []);
    let miss: string[] = m.get_or("zz", ["x" + "y"]);
    let got: string = "none";
    match (alias.get("k1")) { Some(v) => { got = v[0]; }, None => {} }
    print(total.to_string() + " " + hit[0] + " " + miss[0] + " " + got + " " + m2.len().to_string() + " " + r_had.to_string());
    return 0;
}
`

func TestSelfHostRoutedScalarMaps(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
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
		{"keyed_keys", routedMapKeyedKeysSrc, "1635 twonamed-dot four! 19 3 1761 202 two 0 50000000010 1500"},
		{"generic_keyed_keys", routedMapGenericKeyedKeysSrc, "784 twoempty-five! 31 1745"},
		{"map_values", routedMapMapValuesSrc, "5706 8136 0"},
		{"usize_values", routedMapUsizeValuesSrc, "true 3 598 2 0 299"},
		{"usize_keys", routedMapUsizeKeysSrc, "1896 299 300 true"},
		{"char_values", routedMapCharValuesSrc, "122946,63 1068 true"},
		{"view_values", routedMapViewValuesSrc, "abc,over,gh7!,h7,lit,ite,abcdefgh7!,miss,gone,cde,a,true,abcdefgh7!,none,again,abcdefgh7!,cde,pt,abcdefgh7!,1\n2749"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stdout, stderr := routedMapRun(t, selfHostBin, stdlibRoot, c.src, target, "FERN_SANITIZE=1")
					if stdout != c.want {
						t.Fatalf("stdout = %q, want %q\n%s", stdout, c.want, stderr)
					}
					if !strings.Contains(stderr, "leakcheck:") {
						t.Fatalf("no leak census line\n%s", stderr)
					}
					if strings.Contains(stderr, "fern-sanitizer:") || (strings.Contains(stderr, "leakcheck:") && !strings.Contains(stderr, "live_bytes=0")) {
						t.Fatalf("heap finding:\n%s", stderr)
					}
				})
			}
		})
	}

	for _, c := range cases {
		asm := routedMapAsm(t, selfHostBin, stdlibRoot, c.src)
		if !strings.Contains(asm, "call __fn___map_put_") {
			t.Fatalf("the %s program's maps are not routed onto core/map", c.name)
		}
	}
}

func routedMapAsm(t *testing.T, fernBin, stdlibRoot, src string) string {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog.s")
	cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", out, in, stdlibRoot)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, msg)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func routedMapRun(t *testing.T, fernBin, stdlibRoot, src, target string, env ...string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	args := []string{"-target", target, "-o", out, in, stdlibRoot}
	if target == "wasm32-wasi" {
		out = filepath.Join(dir, "prog.wat")
		args = []string{"-target", target, "-emit", "asm", "-o", out, in, stdlibRoot}
	}
	cmd := exec.Command(fernBin, args...)
	cmd.Env = append(append(os.Environ(), "FERN_STRICT_IR=1"), env...)
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
		e2eharness.Wasmtime(t)
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
