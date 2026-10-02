package e2eselfhost

import "testing"

// A value-position if, match or block handed straight to a consumer that does
// not bind it — a borrowing call's argument, a `.len()` receiver, an index or
// field read — is released there when it is owned whichever arm ran (#10438):
// a local its arm retained, or a fresh value. Arrays, strings, structs, and a
// struct handed to a `dyn` parameter, through each form and each consumer.
const condArrReleaseSrc = `function sum(xs: i32[]): i32 { let t: i32 = 0; for x in xs { t = t + x; } return t; }
function mk(j: i32): i32[] { return [j, j, j]; }
function main(): i32 {
    let a = [7, 5];
    a = a.append(1);
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        t = t + sum([j, 1]);
        t = t + sum(if (j > 1) { [j] } else { [1, j] });
        t = t + sum(if (j > 1) { mk(j) } else { a });
        t = t + sum(match (j) { 0 => a, _ => [j, j] });
        t = t + sum(if (j > 0) { if (j > 1) { a } else { mk(j) } } else { [4] });
        t = t + sum({ let q = [j, 1]; q });
        t = t + sum({ let q = a; q });
        t = t + sum({ let k = j + 1; [k, k] });
        t = t + (if (j > 1) { a } else { [1, 2] }).len();
        t = t + (match (j) { 0 => a, _ => [j, j] }).len();
        t = t + ({ let q = [j, 1]; q }).len();
        t = t + (if (j > 1) { a } else { mk(j) })[0];
        t = t + (match (j) { 0 => a, _ => [j, j] })[0];
        t = t + ({ let q = [j, 1]; q })[0];
        j = j + 1;
    }
    return (t + a[0]) % 101;
}
`

const condStrReleaseSrc = `function slen(s: string): i32 { return s.len(); }
function tail(): string { return "xyz"; }
function mks(j: i32): string { if (j > 0) { return "kk" + tail(); } return "k" + tail(); }
function main(): i32 {
    let s = "abc" + tail();
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        t = t + slen(if (j > 1) { mks(j) } else { s + "x" });
        t = t + slen(match (j) { 0 => mks(j), _ => s + "y" });
        t = t + slen({ let q = s + "z"; q });
        t = t + (if (j > 1) { mks(j) } else { s + "x" }).len();
        t = t + (match (j) { 0 => mks(j), _ => s + "y" }).len();
        t = t + ({ let q = s + "z"; q }).len();
        t = t + ((if (j > 1) { mks(j) } else { s + "x" })[1] as i32);
        t = t + ((match (j) { 0 => mks(j), _ => s + "y" })[1] as i32);
        t = t + (({ let q = s + "z"; q })[1] as i32);
        t = t + ((s + "w")[2] as i32);
        j = j + 1;
    }
    return (t + s.len()) % 101;
}
`

const condStructReleaseSrc = `struct P { x: i32, y: i32 }
trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function measure(d: dyn Shape): i32 { return d.area(); }
function px(p: P): i32 { return p.x; }
function mkp(j: i32): P { return P { x: j, y: 4 }; }
function mksq(j: i32): Square { return Square { side: j }; }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        t = t + px(if (j > 1) { mkp(j) } else { P { x: 3, y: j } });
        t = t + px(match (j) { 0 => mkp(j), _ => P { x: 7, y: j } });
        t = t + px({ let q = P { x: j, y: 6 }; q });
        t = t + (if (j > 1) { mkp(j) } else { P { x: 4, y: j } }).y;
        t = t + (match (j) { 0 => mkp(j), _ => P { x: 7, y: j } }).x;
        t = t + ({ let q = P { x: j, y: 6 }; q }).y;
        t = t + measure(Square { side: j });
        t = t + measure(if (j > 1) { mksq(j) } else { Square { side: 2 } });
        t = t + measure(match (j) { 0 => mksq(j), _ => Square { side: j } });
        t = t + measure({ let q = Square { side: j }; q });
        j = j + 1;
    }
    return t % 101;
}
`

// A struct array a value block declares keeps its element credit, and an
// element bound out of one (`let p0 = qs[0]`) is a counted share rather than an
// escape, so the array still releases its elements (#10438).
const condStructArrLocalsSrc = `struct P { x: i32, y: i32 }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 10) {
        let n = { let qa = [P { x: 5, y: j }]; qa[0].y };
        let m = { let qb = [P { x: 5, y: j }]; let pb = qb[0]; t = t + pb.x; qb };
        let qs = [P { x: 5, y: j }, P { x: 6, y: j + 1 }];
        let p0 = qs[0];
        if (j > 3) {
            let p1 = qs[1];
            t = t + p1.y;
        }
        t = t + n + m[0].y + p0.x + qs[1].y;
        j = j + 1;
    }
    return t % 101;
}
`

// A struct array passed to a callee that hands one of its elements back — a
// struct return, a generic one, a dyn one — takes no element credit: the walk
// would free the box the caller now holds. It used to take the credit on the
// box flag alone, and the freed box then read as the next allocation.
const condElemHandoutSrc = `struct P { x: i32, y: i32 }
trait Shape { function area(self: Self): i32; }
impl Shape for P { function area(self: Self): i32 { return self.x * self.y; } }
function keep(q: P[], i: i32): P { return q[i]; }
function get[T](xs: T[], i: i32): T { return xs[i]; }
function pick(q: P[], i: i32): dyn Shape { return q[i]; }
function mkp(j: i32): P { return P { x: 1000, y: j }; }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    let a: P = P { x: 0, y: 0 };
    let b: P = P { x: 0, y: 0 };
    let c: dyn Shape = P { x: 0, y: 0 };
    while (j < 10) {
        let q = [P { x: 5, y: j + 100 }, P { x: 6, y: j + 1 }];
        let z1 = mkp(j);
        let z2 = mkp(j + 1);
        let z3 = mkp(j + 2);
        t = t + a.y + b.x + c.area() + z1.y + z2.y + z3.y + q[1].y;
        a = keep(q, 1);
        b = get(q, 0);
        c = pick(q, 1);
        j = j + 1;
    }
    return t % 101;
}
`

// An arm that yields a string, struct or dyn local declared outside the
// conditional retains it, so a mix of such arms and fresh ones is owned
// whichever arm ran (#10570): released by its consumer, credited as a binding,
// and a counted return. The yielded local keeps its own release.
const condAliasStrSrc = `function slen(s: string): i32 { return s.len(); }
function tail(): string { return "xyz"; }
function rets(j: i32): string { let s = "r" + tail(); return if (j > 1) { s } else { s + "q" }; }
function retm(j: i32): string { let s = "r" + tail(); return match (j) { 0 => s + "m", _ => s }; }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let s = "abc" + tail();
        t = t + slen(if (j > 1) { s } else { s + "x" });
        t = t + slen(match (j) { 0 => s + "y", _ => s });
        t = t + slen({ let k = j; if (k > 1) { s } else { s + "z" } });
        t = t + (if (j > 0) { s } else { s + "w" }).len();
        t = t + ((match (j) { 1 => s, _ => s + "v" })[1] as i32);
        let b = if (j > 1) { s } else { s + "x" };
        let m = match (j) { 0 => s + "m", _ => s };
        t = t + b.len() + m.len();
        t = t + rets(j).len();
        let r = retm(j);
        t = t + r.len();
        j = j + 1;
    }
    return t % 101;
}
`

const condAliasStructSrc = `struct P { x: i32, y: i32 }
function px(p: P): i32 { return p.x; }
function mkp(j: i32): P { return P { x: j, y: 4 }; }
function retp(j: i32): P { let p = P { x: j, y: 7 }; return if (j > 1) { p } else { mkp(j) }; }
function retq(j: i32): P { let p = P { x: j, y: 8 }; return match (j) { 0 => P { x: 1, y: j }, _ => p }; }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let p = P { x: j, y: 2 };
        t = t + px(if (j > 1) { p } else { mkp(j) });
        t = t + px(match (j) { 0 => P { x: 6, y: j }, _ => p });
        t = t + (if (j > 1) { p } else { P { x: 5, y: j } }).y;
        t = t + (match (j) { 1 => p, _ => mkp(j) }).x;
        let q = if (j > 0) { p } else { mkp(j) };
        let w = match (j) { 2 => p, _ => P { x: 9, y: j } };
        t = t + q.y + w.x;
        t = t + retp(j).y;
        let rp = retq(j);
        t = t + rp.x;
        j = j + 1;
    }
    return t % 101;
}
`

// The fresh dyn arm is a local the arm declares and yields; a dyn producer's
// result is not provably owned yet (#10572), and neither is a dyn local
// re-declared in a loop body, so the bindings sit in functions of their own.
const condAliasDynSrc = `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function measure(d: dyn Shape): i32 { return d.area(); }
function bind_one(j: i32): i32 {
    let d: dyn Shape = Square { side: j + 2 };
    let e: dyn Shape = if (j > 1) { d } else { let z: dyn Shape = Square { side: j }; z };
    return e.area() + d.area();
}
function bind_two(j: i32): i32 {
    let d: dyn Shape = Square { side: j + 2 };
    let d2: dyn Shape = Square { side: j + 5 };
    let e: dyn Shape = match (j) { 0 => d2, _ => d };
    return e.area() + measure(if (j > 1) { d } else { d2 });
}
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    let d: dyn Shape = Square { side: 3 + t };
    while (j < 3) {
        t = t + measure(if (j > 1) { d } else { let z: dyn Shape = Square { side: j }; z });
        t = t + measure(match (j) { 0 => d, _ => { let y: dyn Shape = Square { side: j + 1 }; y } });
        t = t + bind_one(j) + bind_two(j);
        j = j + 1;
    }
    return (t + d.area()) % 101;
}
`

// The yielded locals outlive every conditional that yields them, and a struct
// parameter yielded at a call is retained and released there.
const condAliasOutlivesSrc = `struct P { x: i32, y: i32 }
function slen(s: string): i32 { return s.len(); }
function px(p: P): i32 { return p.x; }
function tail(): string { return "xyz"; }
function mkp(j: i32): P { return P { x: j, y: 4 }; }
function viap(a: P, j: i32): i32 { return px(if (j > 1) { a } else { mkp(j) }); }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    let s = "abc" + tail();
    let p = P { x: 9, y: 2 };
    while (j < 3) {
        let g = if (j > 1) { s } else { s + "g" };
        let pg = if (j > 0) { p } else { mkp(j) };
        t = t + g.len() + pg.x + s.len() + p.y;
        t = t + viap(p, j);
        j = j + 1;
    }
    return (t + s.len() + p.y + slen(s) + px(p)) % 101;
}
`

// A block of a bare name, `{ a }`, is the plain read of `a`: no arm store
// retains it, so no consumer may release it. Releasing it freed `a`'s buffer
// under the local.
const condBareNameBlockSrc = `function sum(xs: i32[]): i32 { let t: i32 = 0; for x in xs { t = t + x; } return t; }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let a = [j, 7];
        t = t + sum({ a });
        t = t + ({ a }).len() + ({ a })[1];
        let b = { a };
        t = t + a[0] + a.len() + b[1];
        j = j + 1;
    }
    return t % 101;
}
`

// Not provably owned, so each has to leak rather than be released under its
// source: a parameter yielded by a conditional reads as escaping to the borrow
// inference (#10571), a dyn producer's arm is not counted (#10572), and a
// block hands its tail alias on without a count of its own.
const condNotOwnedSrc = `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
struct Rect { w: i32, h: i32, tag: string }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h + self.tag.len(); } }
function measure(d: dyn Shape): i32 { return d.area(); }
function slen(s: string): i32 { return s.len(); }
function sum(xs: i32[]): i32 { let t: i32 = 0; for x in xs { t = t + x; } return t; }
function mk(j: i32): i32[] { return [j, j, j]; }
function via_param(a: i32[], b: i32[], c: boolean): i32 { return sum(if (c) { a } else { b }); }
function via_str(a: string, j: i32): i32 { return slen(if (j > 1) { a } else { a + "z" }); }
function mkd(j: i32): dyn Shape { return Rect { w: j, h: 2, tag: "q" + "r" }; }
function main(): i32 {
    let s = "abc" + "def";
    let d1: dyn Shape = Square { side: 3 };
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        t = t + measure(if (j > 0) { d1 } else { mkd(j) });
        t = t + measure(match (j) { 0 => mkd(j), _ => d1 });
        t = t + via_param([j], mk(j), j > 1);
        t = t + via_str(s, j);
        t = t + slen({ let q = s; q });
        j = j + 1;
    }
    return (t + s.len() + d1.area()) % 101;
}
`

// A block tail or an arm that yields a string, struct or nested array is not
// released by its consumer: that release is one buffer dec, which would free
// the buffer and strand its elements; the value is released whole.
const condNonScalarElemSrc = `struct P { x: i32, y: i32 }
function tail(): string { return "xyz"; }
function slen(xs: string[]): i32 { return xs.len(); }
function pn(ps: P[]): i32 { return ps.len(); }
function main(): i32 {
    let s = "abc" + tail();
    let a = [s + "1", s + "2"];
    let ps = [P { x: 1, y: 2 }];
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 10) {
        t = t + ({ let q = [s + "a", s + "b"]; q }).len();
        t = t + ({ let q = [s + "c"]; q })[0].len();
        t = t + slen({ let q = [s + "d"]; q });
        t = t + ({ let q = [P { x: j, y: 1 }, P { x: 2, y: j }]; q }).len();
        t = t + ({ let q = [P { x: j, y: 3 }]; q })[0].y;
        t = t + pn({ let q = [P { x: j, y: 4 }]; q });
        t = t + ({ let q = [[j, 1], [2, j]]; q }).len();
        t = t + ({ let q = [[j, 3]]; q })[0][1];
        t = t + (if (j > 4) { a } else { [s + "e"] }).len();
        t = t + (match (j) { 0 => ps, _ => [P { x: j, y: 5 }] })[0].y;
        j = j + 1;
    }
    return (t + a.len() + ps[0].x) % 101;
}
`

// A row handed through a callee (`g(q[0])`) as a binding, an assignment, a
// discarded statement and an operand: the element box is counted there, so
// the array keeps its element credit and nothing is freed twice.
const condRowHandoutSrc = `struct P { x: i32, y: i32 }
trait Shape { function area(self: Self): i32; }
impl Shape for P { function area(self: Self): i32 { return self.x * self.y; } }
function id(p: P): P { return p; }
function idg[T](v: T): T { return v; }
function todyn(p: P): dyn Shape { return p; }
function mkp(j: i32): P { return P { x: 1000, y: j }; }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    let a: P = P { x: 0, y: 0 };
    let b: P = P { x: 0, y: 0 };
    let c: dyn Shape = P { x: 0, y: 0 };
    while (j < 10) {
        let q = [P { x: 5, y: j + 100 }, P { x: 6, y: j + 1 }];
        let z1 = mkp(j);
        let z2 = mkp(j + 1);
        let z3 = mkp(j + 2);
        t = t + a.y + b.x + c.area() + z1.y + z2.y + z3.y + q[1].y;
        a = id(q[1]);
        b = idg(q[0]);
        c = todyn(q[1]);
        let d = id(q[0]);
        id(q[1]);
        t = t + d.x + id(q[0]).y;
        j = j + 1;
    }
    return t % 101;
}
`

// An Option consumed by a match expression in return position is released by
// that match, whether or not its payload's source stays live. The value
// block's credit view must not show the match a second time.
const condOptionMatchReturnSrc = `struct P { xs: i32[], k: i32 }
function moved(i: i32): i32 {
    let p: P = P { xs: [i, i + 1], k: i };
    let o: Option[P] = Some(p);
    return (match (o) { Some(q) => q.k, None => 0 });
}
function live(i: i32): i32 {
    let p: P = P { xs: [i, i + 1], k: i };
    let o: Option[P] = Some(p);
    return (match (o) { Some(q) => q.k, None => 0 }) + p.xs[0];
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 10) { t = t + moved(r) + live(r); r = r + 1; }
    return t % 97;
}
`

// A struct array a value block declares ahead of a tail if or match keeps its
// element credit.
const condBlockTailMatchSrc = `struct P { x: i32, y: i32 }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 10) {
        let n = { let qa = [P { x: 5, y: j }]; match (j) { 0 => qa[0].y, _ => qa[0].x } };
        let m = { let qb = [P { x: 5, y: j }]; if (j > 3) { qb[0].x } else { qb[0].y } };
        t = t + n + m;
        j = j + 1;
    }
    return t % 101;
}
`

// A struct local rebound from a generic identity over a FRESH value owns the
// result: nothing else holds what comes back, so it keeps its credit.
const condHandbackFreshSrc = `struct P { x: i32, y: i32 }
function idg[T](v: T): T { return v; }
function mkp(j: i32): P { return P { x: 1000, y: j }; }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    let b: P = P { x: 0, y: 0 };
    while (j < 10) {
        b = idg(mkp(j + 1));
        t = t + b.y;
        j = j + 1;
    }
    return t % 101;
}
`

// Interpreter-confirmed answers.
var condReleaseCases = []struct {
	name     string
	src      string
	want     int
	balanced bool
}{
	{"array", condArrReleaseSrc, 86, true},
	{"string", condStrReleaseSrc, 19, true},
	{"struct", condStructReleaseSrc, 89, true},
	{"struct_array_locals", condStructArrLocalsSrc, 88, true},
	{"element_handout", condElemHandoutSrc, 75, true},
	{"alias_string", condAliasStrSrc, 34, true},
	{"alias_struct", condAliasStructSrc, 65, true},
	{"alias_dyn", condAliasDynSrc, 12, true},
	{"alias_outlives", condAliasOutlivesSrc, 95, true},
	{"bare_name_block", condBareNameBlockSrc, 81, true},
	{"not_owned", condNotOwnedSrc, 98, false},
	{"nonscalar_elements", condNonScalarElemSrc, 73, true},
	{"row_handout", condRowHandoutSrc, 59, true},
	{"option_match_return", condOptionMatchReturnSrc, 38, true},
	{"block_tail_match_credit", condBlockTailMatchSrc, 81, true},
	{"handback_fresh", condHandbackFreshSrc, 55, true},
}

func TestSelfHostConditionalValueReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range condReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := cli.exitOf(t, tc.src, "x86-64-linux", "FERN_LEAKCHECK=1")
			if exit != tc.want {
				t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			balanced := assertCondCensus(t, stderr, tc.balanced)
			stderr, exit = cli.exitOf(t, tc.src, "x86-64-linux", "FERN_SANITIZE=1")
			if exit != tc.want || forArrStructSanitizerFault(stderr, balanced) {
				t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer fault\n%s", exit, tc.want, stderr)
			}
		})
	}
}

func TestSelfHostConditionalValueReleaseArm64(t *testing.T) {
	checkConditionalValueRelease(t, "arm64-linux")
}

func TestSelfHostConditionalValueReleaseWasm(t *testing.T) {
	checkConditionalValueRelease(t, "wasm32-wasi")
}

func checkConditionalValueRelease(t *testing.T, target string) {
	cli := buildSelfHostCLI(t)
	for _, tc := range condReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := cli.exitOf(t, tc.src, target, "FERN_LEAKCHECK=1")
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertCondCensus(t, stderr, tc.balanced)
		})
	}
}

// assertCondCensus requires a balanced census, or for a program that is
// allowed to leak, no more frees than allocations. It reports whether the
// census had to balance.
func assertCondCensus(t *testing.T, stderr string, balanced bool) bool {
	t.Helper()
	if balanced {
		assertBalancedCensus(t, stderr)
		return true
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil {
		t.Fatalf("no leakcheck summary: %v\n%s", err, stderr)
	}
	if frees > allocs {
		t.Errorf("allocs=%d frees=%d — more released than allocated", allocs, frees)
	}
	return false
}
