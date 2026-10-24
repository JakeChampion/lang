package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

type allocScaleCase struct {
	name  string
	decls string
	n     int

	maxRatio int

	wantQuadratic bool

	boxed bool
}

var allocScaleCases = []allocScaleCase{
	{
		name: "array-append",
		decls: `function churn(n: i32): i32 {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = a.append(i); i = i + 1; }
    return a.len();
}`,
		n:        400,
		maxRatio: 220,
	},
	{
		name: "array-append-through-call",
		decls: `function step(acc: i32[], v: i32): i32[] { return acc.append(v); }
function churn(n: i32): i32 {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = step(a, i); i = i + 1; }
    return a.len();
}`,
		n:        400,
		maxRatio: 220,
	},
	{
		name: "string-parts-join",
		decls: `import "std/string";
function churn(n: i32): i32 {
    let parts: string[] = [];
    let i: i32 = 0;
    while (i < n) { parts = parts.append("item"); i = i + 1; }
    return parts.join(",").len();
}`,
		n:        400,
		maxRatio: 220,
	},
	{
		name: "map-insert",
		decls: `import "std/i32";
import "core/map";
function churn(n: i32): i32 {
    let m: Map[string, i32] = map_new(8);
    let i: i32 = 0;
    while (i < n) { m = m.insert(i.to_string(), i); i = i + 1; }
    return m.len();
}`,
		n:        400,
		maxRatio: 220,
	},
	{
		name: "substring-search",
		decls: `import "std/string";
function churn(n: i32): i32 {
    let hay: string[] = [];
    let i: i32 = 0;
    while (i < n) { hay = hay.append("abcab"); i = i + 1; }
    let s: string = hay.join("");
    let r: i32 = 1;
    match (s.index_of("abcabx")) { Some(i) => { r = r + i + 1; }, None => {} }
    match (s.index_of("bcab")) { Some(i) => { r = r + i; }, None => {} }
    return r;
}`,
		n:        400,
		maxRatio: 220,
	},
	{
		name: "struct-field-array-with",
		decls: `struct P { a: i32, b: i32 }
struct Box { items: P[], tag: i32 }
function churn(n: i32): i32 {
    let b: Box = Box { items: [P { a: 0, b: 0 }, P { a: 0, b: 0 }], tag: 0 };
    let i: i32 = 0;
    while (i < n) {
        b = Box { ...b, items: b.items.with(i % 2, P { a: i, b: i + 1 }) };
        i = i + 1;
    }
    return b.items[0].a + b.items[1].b;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "string-array-with",
		decls: `import "std/i32";
function mks(): string[] {
    let out: string[] = [];
    let i: i32 = 0;
    while (i < 8) { out = out.append("kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; }
    return out;
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let a: string[] = mks();
        a = a.with(3, a[5]);
        t = t + a.len();
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "closure-in-struct-field",
		decls: `import "std/i32";
struct P { name: string, f: (i32) => i32 }
function mkP(n: i32): P { return P { name: "provider" + n.to_string(), f: (x: i32) => x + n }; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < n) {
        let ps: P[] = [];
        let i: i32 = 0;
        while (i < 8) { ps = ps.append(mkP(i)); i = i + 1; }
        let j: i32 = 0;
        while (j < ps.len()) { t = t + (ps[j].f)(1); j = j + 1; }
        r = r + 1;
    }
    return t % 7;
}`,
		n:        200,
		maxRatio: 130,
	},
	{
		name: "closure-call-array-arg",
		decls: `import "std/i32";
function each(f: (i32[]) => i32, n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + f([1, 2, i]); i = i + 1; }
    return t;
}
function churn(n: i32): i32 {
    let h: (i32[]) => i32 = (xs: i32[]) => xs.len();
    return each(h, n) % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "call-result-field-read",
		decls: `import "std/i32";
struct P { a: i32, b: i32 }
struct Box { items: P[], tag: i32 }
function mk_box(i: i32): Box { return Box { items: [P { a: i, b: i }, P { a: i, b: i }], tag: i }; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let a: P[] = mk_box(i).items;
        t = t + a[0].a;
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name:  "struct-update-fresh-base",
		boxed: true,
		decls: `struct R { tag: string, note: string, n: i32 }
function mk(): R { return R { tag: "base", note: "", n: 0 }; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let a: R = R { ...mk(), n: i };
        let b: R = R { ...R { tag: "inner", note: "x", n: 0 }, n: i };
        let c: R = R { ...R { ...mk(), note: "mid" }, n: i };
        t = t + a.n + b.note.len() + c.note.len();
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "pair-form-pointer-payload",
		decls: `struct P { a: i32, b: i32 }
function mkarr(k: i32): Option[i32[]] {
    if (k == 0) { return None; }
    return Some([k, k + 1, k + 2]);
}
function mkstruct(k: i32): Option[P] {
    if (k == 0) { return None; }
    return Some(P { a: k, b: k + 1 });
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        match (mkarr(1)) { Some(a) => { t = t + a[0]; }, None => { }, }
        match (mkstruct(1)) { Some(p) => { t = t + p.b; }, None => { }, }
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "pair-form-tuple-payload",
		decls: `function mkt(k: i32): Option[(i32, i32)] {
    if (k == 0) { return None; }
    return Some((k, k + 1));
}
function mkta(k: i32): Option[(i32[], i32)] {
    if (k == 0) { return None; }
    return Some(([k, k + 1, k + 2], k));
}
function mktn(k: i32): Option[((i32, i32), i32)] {
    if (k == 0) { return None; }
    return Some(((k, k), k));
}
function mkr(k: i32): Result[(i32, i32), (i32, i32)] {
    if (k == 0) { return Err((0, 0)); }
    return Ok((k, k + 1));
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        match (mkt(1))  { Some(a) => { t = t + a.0; }, None => { }, }
        match (mkta(1)) { Some(a) => { t = t + a.1; }, None => { }, }
        match (mktn(1)) { Some(a) => { t = t + a.1; }, None => { }, }
        match (mkr(1))  { Ok(a) => { t = t + a.0; }, Err(e) => { t = t + e.0; }, }
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "pair-form-payload-borrowing-call",
		decls: `struct P { a: i32, b: i32 }
function mkarr(k: i32): Option[i32[]] {
    if (k == 0) { return None; }
    return Some([k, k + 1, k + 2]);
}
function mkstruct(k: i32): Option[P] {
    if (k == 0) { return None; }
    return Some(P { a: k, b: k + 1 });
}
function total(a: i32[]): i32 { let s: i32 = 0; let i: i32 = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }
function pb(p: P): i32 { return p.b; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        match (mkarr(1))    { Some(a) => { t = t + a.len(); }, None => { }, }
        match (mkarr(1))    { Some(a) => { t = t + total(a); }, None => { }, }
        match (mkarr(1))    { Some(a) => { t = t + a[0] + a.len(); }, None => { }, }
        match (mkstruct(1)) { Some(p) => { t = t + pb(p); }, None => { }, }
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "pair-form-enum-temp-as-argument",
		decls: `struct S2 { a: i32, b: i32 }
function mk3(k: i32): Option[i32[]] {
    if (k == 0) { return None; }
    return Some([k, k + 1, k + 2]);
}
function mk20(k: i32): Option[i32[]] {
    if (k == 0) { return None; }
    return Some([k, k, k, k, k, k, k, k, k, k, k, k, k, k, k, k, k, k, k, k]);
}
function mkS(k: i32): Option[S2] {
    if (k == 0) { return None; }
    return Some(S2 { a: k, b: k + 1 });
}
function fac(o: Option[i32[]]): i32 {
    match (o) { Some(a) => { return a[0]; }, None => { return 0; }, }
}
function facS(o: Option[S2]): i32 {
    match (o) { Some(s) => { return s.a; }, None => { return 0; }, }
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        t = t + fac(mk3(1));
        t = t + fac(mk20(1));
        t = t + facS(mkS(1));
        let o: Option[i32[]] = mk3(1);
        t = t + fac(o);
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "fresh-container-read-chained",
		decls: `struct P { a: i32, b: i32 }
struct Box { items: P[], tag: i32 }
struct SBox { names: string[], tag: i32 }
struct Q { vals: i32[], k: i32 }
struct QBox { qs: Q[], tag: i32 }
function mk_box(k: i32): Box {
    return Box { items: [P { a: k, b: k + 1 }, P { a: k + 2, b: k + 3 }], tag: k };
}
function mk_sbox(k: i32): SBox {
    return SBox { names: ["nn", "mm"], tag: k };
}
function mk_qbox(k: i32): QBox {
    return QBox { qs: [Q { vals: [k, k + 1, k + 2], k: k }, Q { vals: [k], k: k }], tag: k };
}
function sink(ps: P[]): i32 { return ps.len(); }
function sinks(ns: string[]): i32 { return ns.len(); }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        t = t + sink(mk_box(1).items);
        t = t + sinks(mk_sbox(1).names);
        t = t + mk_box(2).items[0].a;
        t = t + mk_sbox(3).names[1].len();
        t = t + mk_qbox(4).qs[0].vals[1];
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name:  "tuple-struct-element-binding",
		boxed: true,
		decls: `struct P { a: i32, b: i32, c: i32 }
function pull(s: P): (i32, P) { return (s.a, P { a: s.a + 1, b: s.b, c: s.c }); }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let s: P = P { a: 0, b: 0, c: 0 };
    let i: i32 = 0;
    while (i < n) {
        let p: (i32, P) = pull(s);
        let q: P = p.1;
        t = t + q.a;
        s = q;
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "array-struct-element-binding",
		decls: `import "std/i32";
struct Entry { key: string, value: string }
function wide(k: i32): string { return "a-value-well-past-the-inline-threshold-" + k.to_string(); }
function mk(k: i32): Entry[] {
    let es: Entry[] = [];
    let i: i32 = 0;
    while (i < 4) { es = es.append(Entry { key: wide(k + i), value: wide(k + i + 100) }); i = i + 1; }
    return es;
}
function probe(k: i32): i32 {
    let es: Entry[] = mk(k);
    let a: Entry = es[0];
    return es.len() + a.key.len();
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + probe(i); i = i + 1; }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "fresh-array-into-constructor",
		decls: `struct Node { name: string, deps: string[], mtime: i32, exists: boolean }
function no_deps(): string[] { let e: string[] = []; return e; }
function node(name: string, deps: string[], mtime: i32): Node {
    return Node { name: name, deps: deps, mtime: mtime, exists: true };
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let out: Node[] = [];
        out = out.append(node("alpha", no_deps(), i));
        out = out.append(node("beta", no_deps(), i + 1));
        t = t + out.len();
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "param-append-consumed",
		decls: `function sink(xs: i32[]): i32 { return xs.len(); }
function walk(path: i32[]): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 8) { t = t + sink(path.append(i)); i = i + 1; }
    return t;
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let seed: i32[] = [0];
        t = t + walk(seed) + sink(seed.append(i));
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "array-element-append-consumed",
		decls: `function sink(xs: i32[]): i32 { return xs.len(); }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let xs: i32[][] = [[0], [1]];
        t = t + sink(xs[0].append(9)) + sink(xs[1].append(9)) + sink(xs[0]);
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "with-on-fresh-container-read",
		decls: `struct S { xs: i32[], tag: i32 }
function mk(): S { return S { xs: [1, 2], tag: 0 }; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let b: i32[] = mk().xs.with(0, i);
        t = t + b[0];
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "struct-string-field-move",
		decls: `import "std/i32";
struct W { name: string, n: i32 }
struct Acc { last: string, total: i32 }
function mk(k: i32): W {
    let s: string = "payload-string-" + k.to_string();
    return W { name: s, n: k };
}
function step(a: Acc, k: i32): Acc {
    let s: string = "acc-payload-" + k.to_string();
    return Acc { last: s, total: a.total + k };
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let a: Acc = Acc { last: "", total: 0 };
    let i: i32 = 0;
    while (i < n) {
        let w: W = mk(i % 8);
        a = step(a, i % 8);
        t = t + w.name.len() + a.last.len();
        i = i + 1;
    }
    return (t + a.total) % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "method-identity-return-result",
		decls: `import "std/i32";
import "std/string";
function (s: string) tail(n: i32): str {
    if (n <= 0) { return s; }
    let sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen);
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let base: string = "long-enough-payload-" + (i % 8).to_string();
        t = t + base.tail(2).len() + base.tail(3).to_owned().len();
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "nested-string-array-drop",
		decls: `import "std/i32";
function mk(k: i32): string[][] {
    let inner: string[] = ["alpha-payload-" + k.to_string(), "beta-payload-" + k.to_string()];
    return [inner, ["gamma-payload-" + k.to_string()]];
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let a: string[][] = mk(i % 8);
        t = t + a[0][0].len() + a[1][0].len();
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name:  "match-returning-arms-boxed-result",
		boxed: true,
		decls: `function make(i: i64): Result[i64, i64] {
    if (i % 2i64 == 0i64) { return Ok(i); }
    return Err(i);
}
function pick(i: i64): i32 {
    match (make(i)) { Ok(v) => { return (v as i32) + 1; }, Err(_) => { return 0; } }
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + pick(i as i64); i = i + 1; }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "match-returning-arms-map-get",
		decls: `import "core/map";
function pick(m: Map[string, i32], k: string): i32 {
    match (m.get(k)) { Some(v) => { return v + 1; }, None => { return 0; } }
}
function churn(n: i32): i32 {
    let m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + pick(m, "a"); i = i + 1; }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		name: "state-threading-chain",
		decls: `struct Op { tag: i32, note: string }
struct St { ops: Op[], names: string[], ctrl: i32, who: string }
function (s: St) emit(op: Op): St {
    return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 };
}
function step(s: St, k: i32): St {
    let a: St = s.emit(Op { tag: k, note: "" });
    let b: St = a.emit(Op { tag: k + 1, note: "" });
    let c: St = b.emit(Op { tag: k + 2, note: "" });
    let d: St = c.emit(Op { tag: k + 3, note: "" });
    return d;
}
function churn(n: i32): i32 {
    let s: St = St { ops: [], names: [], ctrl: 0, who: "x" };
    let i: i32 = 0;
    while (i < n) { s = step(s, i * 4); i = i + 1; }
    return s.ops.len() + s.ctrl;
}`,
		n:        400,
		maxRatio: 220,
	},
	{
		name: "string-concat-fold",
		decls: `import "std/string";
function churn(n: i32): i32 {
    let s: string = "";
    let i: i32 = 0;
    while (i < n) { s = s + "x"; i = i + 1; }
    return s.len();
}`,
		n:        400,
		maxRatio: 220,
	},
	{
		name: "string-prepend-fold",
		decls: `import "std/string";
function churn(n: i32): i32 {
    let s: string = "";
    let i: i32 = 0;
    while (i < n) { s = "x" + s; i = i + 1; }
    return s.len();
}`,
		n:             400,
		maxRatio:      220,
		wantQuadratic: true,
	},
}

func (c allocScaleCase) volumeSrc(n int) string {
	return fmt.Sprintf(`import "std/i64";
%s
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let w: i32 = churn(%d);
    let b1: i64 = __heap_bump_bytes();
    if (w < 0) { return 251; }
    print("VOLUME " + (b1 - b0).to_string());
    return 0;
}
`, c.decls, n)
}

func parseVolume(t *testing.T, label, out string, exit int) int64 {
	t.Helper()
	if exit == 251 {
		t.Fatalf("%s: churn returned a negative value — the probe is not measuring the work it thinks it is", label)
	}
	if exit != 0 {
		t.Fatalf("%s: probe exited %d, want 0\noutput:\n%s", label, exit, out)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "VOLUME ") {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimPrefix(line, "VOLUME "), 10, 64)
		if err != nil {
			t.Fatalf("%s: unparseable VOLUME line %q: %v", label, line, err)
		}
		return v
	}
	t.Fatalf("%s: no VOLUME line in output:\n%s", label, out)
	return 0
}

func TestX86_64AllocScaling(t *testing.T) {
	for _, tc := range allocScaleCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.boxed {
				e2eharness.BoxedProbes(t)
			}
			out1, exit1 := compileAndRunX86_64(t, tc.volumeSrc(tc.n))
			v1 := parseVolume(t, tc.name+"@n", out1, exit1)
			out2, exit2 := compileAndRunX86_64(t, tc.volumeSrc(2*tc.n))
			v2 := parseVolume(t, tc.name+"@2n", out2, exit2)

			if v1 <= 0 {
				t.Fatalf("churn(%d) allocated %d bytes — a shape that allocates "+
					"nothing cannot show a scaling regression, so this case is not "+
					"measuring anything", tc.n, v1)
			}

			ratio := int(v2 * 100 / v1)

			t.Logf("n=%d: %d B   2n=%d: %d B   ratio %d.%02dx (bound %d.%02dx)",
				tc.n, v1, 2*tc.n, v2, ratio/100, ratio%100, tc.maxRatio/100, tc.maxRatio%100)

			if tc.wantQuadratic {
				if ratio <= tc.maxRatio {
					t.Errorf("%s is the CALIBRATION case: it is inherently quadratic and "+
						"must measure ABOVE %d.%02dx, but came in at %d.%02dx (%d B -> %d B). "+
						"Either this shape stopped being quadratic — which would be a real "+
						"improvement worth recording by removing wantQuadratic — or the gate "+
						"has stopped discriminating and every other case in this corpus is "+
						"now passing vacuously",
						tc.name, tc.maxRatio/100, tc.maxRatio%100, ratio/100, ratio%100, v1, v2)
				}
				return
			}

			if ratio > tc.maxRatio {
				t.Errorf("%s allocation grew %d.%02dx when n doubled (%d B at n=%d -> %d B "+
					"at n=%d), over the %d.%02dx bound. Doubling the input should at most "+
					"double the memory; this is superlinear, which is the O(n) -> O(n^2) "+
					"class of regression that leaves every correctness test green while "+
					"making the shape unusable at real sizes",
					tc.name, ratio/100, ratio%100, v1, tc.n, v2, 2*tc.n,
					tc.maxRatio/100, tc.maxRatio%100)
			}
		})
	}
}
