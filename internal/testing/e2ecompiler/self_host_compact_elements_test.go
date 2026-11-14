package e2ecompiler

import (
	"path/filepath"
	"strings"
	"testing"
)

// An i32, a u32, a char or an f32 array element takes 4 bytes on every target. Each
// program runs on x86-64, arm64 and wasm32 under the sanitizer and the leak
// census, and its output is what the Go interpreter prints for it.

const compactElemsI32Src = `import "std/i32";
import "std/i64";
import "std/array";
import "core/cmp";

function total(xs: [i32]): i64 {
    let t: i64 = 0;
    for x in xs { t = t * 31 + (x as i64); }
    return t;
}

function mk(): i32[] { return [7, -8, 9]; }

function main(): i32 {
    let lit: i32[] = [1, -2, 2147483647, -2147483648];
    print("lit " + lit.len().to_string() + " " + total(lit).to_string() + " " + lit[3].to_string() + " " + lit[2].to_string());
    let grown: i32[] = [];
    let i: i32 = 0;
    while (i < 1000) { grown = grown.append(i * 3 - 1500); i = i + 1; }
    print("grown " + grown.len().to_string() + " " + total(grown).to_string() + " " + grown[999].to_string() + " " + grown[0].to_string());
    let shared: i32[] = grown;
    grown = grown.with(3, -77);
    let w: i32[] = grown.with(4, 88);
    print("with " + grown[3].to_string() + " " + w[4].to_string() + " " + grown[4].to_string() + " " + shared[3].to_string());
    let sl: [i32] = grown[10:20];
    let inner: [i32] = sl[2:5];
    print("view " + sl.len().to_string() + " " + total(sl).to_string() + " " + inner[0].to_string() + " " + total(inner).to_string());
    let cp: i32[] = array.slice(grown, 5, 9);
    print("slice " + cp.len().to_string() + " " + total(cp).to_string());
    let z: i32[] = __alloc_i32(13);
    z = z.with(12, -9);
    print("alloc " + z.len().to_string() + " " + total(z).to_string() + " " + z[11].to_string());
    let cat: i32[] = lit.concat(mk());
    let cat2: i32[] = grown.concat(grown[0:3]);
    print("concat " + cat.len().to_string() + " " + total(cat).to_string() + " " + cat2.len().to_string() + " " + cat2[1001].to_string());
    print("eq " + (mk() == [7, -8, 9]).to_string() + " " + (mk() == [7, -8, 10]).to_string() + " " + (cp == array.slice(grown, 5, 9)).to_string());
    let s: i32[] = cmp.sort([5, -3, 9, 0, -2147483648, 2147483647]);
    print("sort " + s[0].to_string() + " " + s[1].to_string() + " " + s[5].to_string());
    let r: i32[] = array.reverse(lit);
    print("rev " + r[0].to_string() + " " + r[3].to_string());
    let t: i32[] = grown.truncate(10);
    print("truncate " + t.len().to_string() + " " + total(t).to_string());
    let ext: i64[] = [];
    for x in lit { ext = ext.append(x as i64); }
    print("ext " + ext[1].to_string() + " " + ext[3].to_string() + " " + (lit[3] >> 1).to_string() + " " + (lit[1] / 2).to_string());
    let sq: i32[] = [];
    for k in 0..300000 { sq = sq.append(k - 150000); }
    print("large " + total(sq).to_string() + " " + sq[299999].to_string() + " " + sq.with(0, 5)[0].to_string());
    return 0;
}
`

const compactElemsI32Want = "lit 4 64424537278 -2147483648 2147483647\n" +
	"grown 1000 1964091552593721308 1497 -1500\n" +
	"with -77 88 -1488 -1491\n" +
	"view 10 -40159053967769265 -1464 -1453653\n" +
	"slice 4 -45711162\n" +
	"alloc 13 -9 0\n" +
	"concat 7 1919271390055386 1003 -1497\n" +
	"eq true false true\n" +
	"sort -2147483648 -3 2147483647\n" +
	"rev -2147483648 1\n" +
	"truncate 10 -40977427324545131\n" +
	"ext -2 -2147483648 -1073741824 -1\n" +
	"large -3059224828054320656 149999 5"

const compactElemsU32CharSrc = `import "std/i32";
import "std/i64";
import "std/u32";
import "std/array";
import "core/cmp";

function utotal(xs: [u32]): i64 {
    let t: i64 = 0;
    for x in xs { t = t * 31 + (x as i64); }
    return t;
}

function ctotal(xs: [char]): i64 {
    let t: i64 = 0;
    for x in xs { t = t * 31 + ((x as i32) as i64); }
    return t;
}

function main(): i32 {
    let u: u32[] = [1 as u32, 4294967295 as u32, 2147483648 as u32];
    u = u.append(7 as u32);
    print("u32 " + u.len().to_string() + " " + utotal(u).to_string() + " " + u[1].to_string() + " " + u[2].to_string() + " " + (u[1] > u[0]).to_string());
    let uw: i64[] = [];
    for x in u { uw = uw.append(x as i64); }
    print("u32 ext " + uw[1].to_string() + " " + uw[2].to_string() + " " + (u[1] >> (1 as u32)).to_string() + " " + (u[2] / (3 as u32)).to_string());
    let uv: [u32] = u[1:3];
    let us: u32[] = array.slice(u, 1, 4);
    print("u32 view " + uv[0].to_string() + " " + utotal(uv).to_string() + " " + utotal(us).to_string());
    let uc: u32[] = u.with(0, 4000000000 as u32);
    print("u32 with " + uc[0].to_string() + " " + u[0].to_string() + " " + utotal(u.concat(uc)).to_string());
    let su: u32[] = cmp.sort([3 as u32, 4294967295 as u32, 0 as u32, 2147483648 as u32]);
    print("u32 sort " + su[0].to_string() + " " + su[2].to_string() + " " + su[3].to_string() + " " + (su == [0 as u32, 3 as u32, 2147483648 as u32, 4294967295 as u32]).to_string());
    let ug: u32[] = [];
    for k in 0..100000 { ug = ug.append((k as u32) * (40000 as u32)); }
    print("u32 grown " + utotal(ug).to_string() + " " + ug[99999].to_string());
    let ch: char[] = ['a', 'z', '\u{1F600}'];
    ch = ch.append('\u{10FFFF}');
    print("char " + ch.len().to_string() + " " + ctotal(ch).to_string() + " " + (ch[2] as i32).to_string() + " " + (ch[3] as i32).to_string());
    let cv: [char] = ch[1:3];
    let cw: char[] = ch.with(0, 'b');
    print("char view " + ctotal(cv).to_string() + " " + (cw[0] as i32).to_string() + " " + (ch[0] as i32).to_string() + " " + (cw == ch).to_string());
    return 0;
}
`

const compactElemsU32CharWant = "u32 4 4194035593381 4294967295 2147483648 true\n" +
	"u32 ext 4294967295 2147483648 2147483647 715827882\n" +
	"u32 view 4294967295 135291469793 4194035563590\n" +
	"u32 with 4000000000 1 3873403303270378091\n" +
	"u32 sort 0 2147483648 4294967295 true\n" +
	"u32 grown -6960214419375811584 3999960000\n" +
	"char 4 8104952 128512 1114111\n" +
	"char view 132294 98 97 false"

const compactElemsContainersSrc = `import "std/i32";
import "std/i64";
import "std/u32";
import "core/map";

struct Rec { tag: i32, data: i32[] }

struct Bag[T] { items: T[], n: i32 }

function total(xs: i32[]): i64 {
    let t: i64 = 0;
    for x in xs { t = t * 31 + (x as i64); }
    return t;
}

function bag[T](items: T[]): Bag[T] {
    return Bag { items: items, n: items.len() };
}

function last[T](xs: T[]): T {
    return xs[xs.len() - 1];
}

function main(): i32 {
    let c: Rec = Rec { tag: 9, data: [4, 5] };
    c = Rec { ...c, data: c.data.append(-6) };
    print("rec " + c.tag.to_string() + " " + total(c.data).to_string());
    let b = bag([-1, 2, -3]);
    let bs = bag(["p", "q"]);
    let bu = bag([4294967295 as u32]);
    print("generic " + b.n.to_string() + " " + last(b.items).to_string() + " " + last(bs.items) + " " + last(bu.items).to_string());
    let nested: i32[][] = [c.data, [7, -7], b.items];
    print("nested " + total(nested[1]).to_string() + " " + nested[2][2].to_string());
    let tup: (i32, i32[]) = (4, [9, -9]);
    let o: Option[i32[]] = Some(tup.1);
    match (o) { Some(a) => { print("opt " + total(a).to_string() + " " + tup.0.to_string()); }, None => { print("none"); } }
    let m: Map[i32, i32] = Map {};
    m = m.insert(-3, 30);
    m = m.insert(2000000000, -2000);
    m = m.insert(7, 70);
    print("keys " + m.keys().len().to_string() + " " + total(m.keys()).to_string() + " " + total(m.values()).to_string());
    let mu: Map[u32, i32] = Map {};
    mu = mu.insert(4294967295 as u32, 1);
    mu = mu.insert(5 as u32, 3);
    let uk: u32[] = mu.keys();
    print("ukeys " + uk.len().to_string() + " " + (uk[0] as i64 + uk[1] as i64).to_string());
    let mc: Map[char, i32] = Map {};
    mc = mc.insert('x', 1);
    mc = mc.insert('\u{10FFFF}', 2);
    let ck: char[] = mc.keys();
    print("ckeys " + ck.len().to_string() + " " + ((ck[0] as i32) + (ck[1] as i32)).to_string());
    let base: i32 = -5;
    let shift = (x: i32) => x + base;
    let xs: i32[] = [1, 2, 3];
    let ys: i32[] = [];
    for x in xs { ys = ys.append(shift(x)); }
    let held = xs;
    let read = (i: i32) => held[i] * 10;
    print("closure " + total(ys).to_string() + " " + read(2).to_string());
    return 0;
}
`

const compactElemsContainersWant = "rec 9 3993\n" +
	"generic 3 -3 q 4294967295\n" +
	"nested 210 -3\n" +
	"opt 270 4\n" +
	"keys 3 61999997124 -33100\n" +
	"ukeys 2 4294967300\n" +
	"ckeys 2 1114231\n" +
	"closure -3939 30"

// With 8-byte elements each cost below is about twice its bound.
const compactElemsStrideSrc = `import "std/i32";
import "std/i64";

function main(): i32 {
    let before: i64 = __heap_bump_bytes();
    let a: i32[] = __alloc_i32(1048576);
    let alloc_cost: i64 = __heap_bump_bytes() - before;
    before = __heap_bump_bytes();
    let b: u32[] = [];
    let i = 0;
    while (i < 1048576) { b = b.append(i as u32); i += 1; }
    let push_cost: i64 = __heap_bump_bytes() - before;
    before = __heap_bump_bytes();
    let c: char[] = [];
    for k in 0..1048576 { c = c.append('a'); }
    let for_cost: i64 = __heap_bump_bytes() - before;
    before = __heap_bump_bytes();
    let d: i32[] = a.concat(a);
    let concat_cost: i64 = __heap_bump_bytes() - before;
    print("alloc " + (alloc_cost < (6000000 as i64)).to_string());
    print("push " + (push_cost < (12000000 as i64)).to_string());
    print("for " + (for_cost < (6000000 as i64)).to_string());
    print("concat " + (concat_cost < (12000000 as i64)).to_string());
    print("lens " + (a.len() + b.len() + c.len() + d.len()).to_string());
    return 0;
}
`

const compactElemsStrideWant = "alloc true\n" +
	"push true\n" +
	"for true\n" +
	"concat true\n" +
	"lens 5242880"

const compactElemsF32Src = `import "std/i32";
import "std/i64";
import "std/f32";
import "std/f64";
import "std/array";
import "core/cmp";
import "core/map";

struct Rec { tag: i32, data: f32[] }

function total(xs: [f32]): f64 {
    let t: f64 = 0.0;
    for x in xs { t = t * 1.5 + (x as f64); }
    return t;
}

function bits(x: f32): string {
    return f32_bits(x).to_string();
}

function mk(): f32[] { return [1.5, -2.25, 3.0e38]; }

function last[T](xs: T[]): T {
    return xs[xs.len() - 1];
}

function main(): i32 {
    let lit: f32[] = [0.1, -0.0, 1.0e-45, 3.4028235e38];
    print("lit " + lit.len().to_string() + " " + total(lit).to_string() + " " + bits(lit[0]) + " " + bits(lit[1]) + " " + bits(lit[2]) + " " + bits(lit[3]));
    let nan: f32 = f32_from_bits(2143289345);
    let inf: f32 = f32_from_bits(2139095040);
    let odd: f32[] = [nan, inf, -inf];
    print("nan " + bits(odd[0]) + " " + bits(odd[1]) + " " + bits(odd[2]) + " " + (odd[0] == odd[0]).to_string());
    let grown: f32[] = [];
    let i: i32 = 0;
    while (i < 1000) { grown = grown.append((i as f32) * 0.25 - 100.0); i = i + 1; }
    print("grown " + grown.len().to_string() + " " + total(grown).to_string() + " " + grown[999].to_string() + " " + grown[0].to_string());
    let shared: f32[] = grown;
    grown = grown.with(3, -7.5);
    let w: f32[] = grown.with(4, 8.125);
    print("with " + grown[3].to_string() + " " + w[4].to_string() + " " + grown[4].to_string() + " " + shared[3].to_string());
    let sl: [f32] = grown[10:20];
    let inner: [f32] = sl[2:5];
    print("view " + sl.len().to_string() + " " + total(sl).to_string() + " " + inner[0].to_string());
    let cp: f32[] = array.slice(grown, 5, 9);
    print("slice " + cp.len().to_string() + " " + total(cp).to_string());
    let cat: f32[] = lit.concat(mk());
    print("concat " + cat.len().to_string() + " " + bits(cat[6]) + " " + bits(cat[1]));
    print("eq " + (mk() == [1.5, -2.25, 3.0e38]).to_string() + " " + (mk() == [1.5, -2.25, 3.0]).to_string());
    let unsorted: f32[] = [5.5, -3.25, 9.0, 0.0, -1.0e30];
    let s: f32[] = cmp.sort(unsorted);
    print("sort " + s[0].to_string() + " " + s[1].to_string() + " " + s[4].to_string());
    let r: f32[] = array.reverse(lit);
    print("rev " + bits(r[0]) + " " + bits(r[3]));
    let sq: f32[] = [];
    for k in 0..100000 { sq = sq.append((k as f32) * 0.5); }
    print("for " + total(sq).to_string() + " " + sq[99999].to_string());
    let c: Rec = Rec { tag: 9, data: [4.0, 5.0] };
    c = Rec { ...c, data: c.data.append(-6.5) };
    print("rec " + c.tag.to_string() + " " + total(c.data).to_string() + " " + last(c.data).to_string());
    let m: Map[string, f32] = Map {};
    m = m.insert("a", 2.5);
    m = m.insert("b", -0.125);
    let vs: f32[] = m.values();
    print("values " + vs.len().to_string() + " " + ((vs[0] as f64) + (vs[1] as f64)).to_string());
    let acc: f32 = 0.0;
    let add = (x: f32) => { acc = acc + x; };
    for x in mk() { add(x); }
    print("cell " + acc.to_string());
    let z: f32[] = __alloc_f32_like(3);
    print("zero " + z.len().to_string() + " " + bits(z[2]));
    return 0;
}

function __alloc_f32_like(n: i32): f32[] {
    let out: f32[] = [];
    for k in 0..n { out = out.append(0.0); }
    return out;
}
`

const compactElemsF32Want = "lit 4 3.4028234663852886e+38 1036831949 -2147483648 1 2139095039\n" +
	"nan 2143289345 2139095040 -8388608 false\n" +
	"grown 1000 -2.4553427878432828e+178 149.75 -100\n" +
	"with -7.5 8.125 -99 -99.25\n" +
	"view 10 -10998.017578125 -97\n" +
	"slice 4 -800.28125\n" +
	"concat 7 2137108966 -2147483648\n" +
	"eq true false\n" +
	"sort -1e+30 -3.25 9\n" +
	"rev 2139095039 1036831949\n" +
	"for Inf 49999.5\n" +
	"rec 9 10 -6.5\n" +
	"values 2 2.375\n" +
	"cell 3e+38\n" +
	"zero 3 0"

const compactElemsF32LanesSrc = `import "std/i32";
import "std/f32";
import "std/f64";

function show(v: f32x4): string {
    return __f32x4_lane(v, 0).to_string() + "," + __f32x4_lane(v, 1).to_string() + "," + __f32x4_lane(v, 2).to_string() + "," + __f32x4_lane(v, 3).to_string();
}

function main(): i32 {
    let xs: f32[] = [];
    for k in 0..12 { xs = xs.append((k as f32) * 1.5 - 4.0); }
    let a = __f32x4_load(xs, 0);
    let b = __f32x4_load(xs, 5);
    print("load " + show(a) + " " + show(b));
    let ys: f32[] = __f32x4_with(xs, 8, __f32x4_splat(-0.5));
    print("with " + ys[7].to_string() + " " + ys[8].to_string() + " " + ys[11].to_string() + " " + xs[8].to_string());
    let d: f64[] = [1.0, 2.0, 3.0];
    let e = __f64x2_load(d, 1);
    print("f64 " + __f64x2_lane(e, 0).to_string() + " " + __f64x2_lane(e, 1).to_string());
    let snan: f32 = f32_from_bits(2139095041);
    let ns: f32[] = [snan];
    print("snan " + f32_bits(ns[0]).to_string());
    return 0;
}
`

const compactElemsF32LanesWant = "load -4,-2.5,-1,0.5 3.5,5,6.5,8\n" +
	"with 6.5 -0.5 -0.5 8\n" +
	"f64 2 3\n" +
	"snan 2143289345"

// With 8-byte elements each cost below is about twice its bound.
const compactElemsF32StrideSrc = `import "std/i32";
import "std/i64";

function main(): i32 {
    let before: i64 = __heap_bump_bytes();
    let xs: f32[] = [];
    let i = 0;
    while (i < 1048576) { xs = xs.append(i as f32); i += 1; }
    let push_cost: i64 = __heap_bump_bytes() - before;
    before = __heap_bump_bytes();
    let ys: f32[] = [];
    for k in 0..1048576 { ys = ys.append(0.5); }
    let for_cost: i64 = __heap_bump_bytes() - before;
    print("push " + (push_cost < (12000000 as i64)).to_string());
    print("for " + (for_cost < (6000000 as i64)).to_string());
    print("lens " + (xs.len() + ys.len()).to_string() + " " + xs[1048575].to_string());
    return 0;
}
`

const compactElemsF32StrideWant = "push true\n" +
	"for true\n" +
	"lens 2097152 1048575"

func TestSelfHostCompactElements(t *testing.T) {
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
		{"i32", compactElemsI32Src, compactElemsI32Want},
		{"u32-char", compactElemsU32CharSrc, compactElemsU32CharWant},
		{"containers", compactElemsContainersSrc, compactElemsContainersWant},
		{"stride", compactElemsStrideSrc, compactElemsStrideWant},
		{"f32", compactElemsF32Src, compactElemsF32Want},
		{"f32-lanes", compactElemsF32LanesSrc, compactElemsF32LanesWant},
		{"f32-stride", compactElemsF32StrideSrc, compactElemsF32StrideWant},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stdout, stderr := routedMapRun(t, selfHostBin, stdlibRoot, c.src, target, "FERN_SANITIZE=1", "FERN_SEM_IR_STRICT=1")
					if stdout != c.want {
						t.Fatalf("stdout = %q, want %q\n%s", stdout, c.want, stderr)
					}
					if strings.Contains(stderr, "fern-sanitizer:") {
						t.Fatalf("heap finding:\n%s", stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		})
	}
}
