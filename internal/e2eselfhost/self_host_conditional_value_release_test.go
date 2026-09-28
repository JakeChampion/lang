package e2eselfhost

import "testing"

// A value-position if, match or block handed straight to a consumer that does
// not bind it — a borrowing call's argument, a `.len()` receiver, an index or
// field read — is released there when it is owned whichever arm ran (#10438):
// a local its arm retained, or a fresh value. The AST lowering left it
// in a temp nothing swept. Arrays, strings, structs, and a struct handed to a
// `dyn` parameter, through each form and each consumer.
const condArrReleaseSrc = `function sum(xs: i32[]): i32 { var t: i32 = 0; for x in xs { t = t + x; } return t; }
function mk(j: i32): i32[] { return [j, j, j]; }
function main(): i32 {
    var a = [7, 5];
    a = a.append(1);
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        t = t + sum([j, 1]);
        t = t + sum(if (j > 1) { [j] } else { [1, j] });
        t = t + sum(if (j > 1) { mk(j) } else { a });
        t = t + sum(match (j) { 0 => a, _ => [j, j] });
        t = t + sum(if (j > 0) { if (j > 1) { a } else { mk(j) } } else { [4] });
        t = t + sum({ var q = [j, 1]; q });
        t = t + sum({ var q = a; q });
        t = t + sum({ var k = j + 1; [k, k] });
        t = t + (if (j > 1) { a } else { [1, 2] }).len();
        t = t + (match (j) { 0 => a, _ => [j, j] }).len();
        t = t + ({ var q = [j, 1]; q }).len();
        t = t + (if (j > 1) { a } else { mk(j) })[0];
        t = t + (match (j) { 0 => a, _ => [j, j] })[0];
        t = t + ({ var q = [j, 1]; q })[0];
        j = j + 1;
    }
    return (t + a[0]) % 101;
}
`

const condStrReleaseSrc = `function slen(s: string): i32 { return s.len(); }
function tail(): string { return "xyz"; }
function mks(j: i32): string { if (j > 0) { return "kk" + tail(); } return "k" + tail(); }
function main(): i32 {
    var s = "abc" + tail();
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        t = t + slen(if (j > 1) { mks(j) } else { s + "x" });
        t = t + slen(match (j) { 0 => mks(j), _ => s + "y" });
        t = t + slen({ var q = s + "z"; q });
        t = t + (if (j > 1) { mks(j) } else { s + "x" }).len();
        t = t + (match (j) { 0 => mks(j), _ => s + "y" }).len();
        t = t + ({ var q = s + "z"; q }).len();
        t = t + ((if (j > 1) { mks(j) } else { s + "x" })[1] as i32);
        t = t + ((match (j) { 0 => mks(j), _ => s + "y" })[1] as i32);
        t = t + (({ var q = s + "z"; q })[1] as i32);
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
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        t = t + px(if (j > 1) { mkp(j) } else { P { x: 3, y: j } });
        t = t + px(match (j) { 0 => mkp(j), _ => P { x: 7, y: j } });
        t = t + px({ var q = P { x: j, y: 6 }; q });
        t = t + (if (j > 1) { mkp(j) } else { P { x: 4, y: j } }).y;
        t = t + (match (j) { 0 => mkp(j), _ => P { x: 7, y: j } }).x;
        t = t + ({ var q = P { x: j, y: 6 }; q }).y;
        t = t + measure(Square { side: j });
        t = t + measure(if (j > 1) { mksq(j) } else { Square { side: 2 } });
        t = t + measure(match (j) { 0 => mksq(j), _ => Square { side: j } });
        t = t + measure({ var q = Square { side: j }; q });
        j = j + 1;
    }
    return t % 101;
}
`

// A struct array a value block declares keeps its element credit, and an
// element bound out of one (`var p0 = qs[0]`) is a counted share rather than an
// escape, so the array still releases its elements (#10438).
const condStructArrLocalsSrc = `struct P { x: i32, y: i32 }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 10) {
        var n = { var qa = [P { x: 5, y: j }]; qa[0].y };
        var m = { var qb = [P { x: 5, y: j }]; var pb = qb[0]; t = t + pb.x; qb };
        var qs = [P { x: 5, y: j }, P { x: 6, y: j + 1 }];
        var p0 = qs[0];
        if (j > 3) {
            var p1 = qs[1];
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
    var t: i32 = 0;
    var j: i32 = 0;
    var a: P = P { x: 0, y: 0 };
    var b: P = P { x: 0, y: 0 };
    var c: dyn Shape = P { x: 0, y: 0 };
    while (j < 10) {
        var q = [P { x: 5, y: j + 100 }, P { x: 6, y: j + 1 }];
        var z1 = mkp(j);
        var z2 = mkp(j + 1);
        var z3 = mkp(j + 2);
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
function rets(j: i32): string { var s = "r" + tail(); return if (j > 1) { s } else { s + "q" }; }
function retm(j: i32): string { var s = "r" + tail(); return match (j) { 0 => s + "m", _ => s }; }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var s = "abc" + tail();
        t = t + slen(if (j > 1) { s } else { s + "x" });
        t = t + slen(match (j) { 0 => s + "y", _ => s });
        t = t + slen({ var k = j; if (k > 1) { s } else { s + "z" } });
        t = t + (if (j > 0) { s } else { s + "w" }).len();
        t = t + ((match (j) { 1 => s, _ => s + "v" })[1] as i32);
        var b = if (j > 1) { s } else { s + "x" };
        var m = match (j) { 0 => s + "m", _ => s };
        t = t + b.len() + m.len();
        t = t + rets(j).len();
        var r = retm(j);
        t = t + r.len();
        j = j + 1;
    }
    return t % 101;
}
`

const condAliasStructSrc = `struct P { x: i32, y: i32 }
function px(p: P): i32 { return p.x; }
function mkp(j: i32): P { return P { x: j, y: 4 }; }
function retp(j: i32): P { var p = P { x: j, y: 7 }; return if (j > 1) { p } else { mkp(j) }; }
function retq(j: i32): P { var p = P { x: j, y: 8 }; return match (j) { 0 => P { x: 1, y: j }, _ => p }; }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var p = P { x: j, y: 2 };
        t = t + px(if (j > 1) { p } else { mkp(j) });
        t = t + px(match (j) { 0 => P { x: 6, y: j }, _ => p });
        t = t + (if (j > 1) { p } else { P { x: 5, y: j } }).y;
        t = t + (match (j) { 1 => p, _ => mkp(j) }).x;
        var q = if (j > 0) { p } else { mkp(j) };
        var w = match (j) { 2 => p, _ => P { x: 9, y: j } };
        t = t + q.y + w.x;
        t = t + retp(j).y;
        var rp = retq(j);
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
    var d: dyn Shape = Square { side: j + 2 };
    var e: dyn Shape = if (j > 1) { d } else { var z: dyn Shape = Square { side: j }; z };
    return e.area() + d.area();
}
function bind_two(j: i32): i32 {
    var d: dyn Shape = Square { side: j + 2 };
    var d2: dyn Shape = Square { side: j + 5 };
    var e: dyn Shape = match (j) { 0 => d2, _ => d };
    return e.area() + measure(if (j > 1) { d } else { d2 });
}
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    var d: dyn Shape = Square { side: 3 + t };
    while (j < 3) {
        t = t + measure(if (j > 1) { d } else { var z: dyn Shape = Square { side: j }; z });
        t = t + measure(match (j) { 0 => d, _ => { var y: dyn Shape = Square { side: j + 1 }; y } });
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
    var t: i32 = 0;
    var j: i32 = 0;
    var s = "abc" + tail();
    var p = P { x: 9, y: 2 };
    while (j < 3) {
        var g = if (j > 1) { s } else { s + "g" };
        var pg = if (j > 0) { p } else { mkp(j) };
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
const condBareNameBlockSrc = `function sum(xs: i32[]): i32 { var t: i32 = 0; for x in xs { t = t + x; } return t; }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var a = [j, 7];
        t = t + sum({ a });
        t = t + ({ a }).len() + ({ a })[1];
        var b = { a };
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
function sum(xs: i32[]): i32 { var t: i32 = 0; for x in xs { t = t + x; } return t; }
function mk(j: i32): i32[] { return [j, j, j]; }
function via_param(a: i32[], b: i32[], c: boolean): i32 { return sum(if (c) { a } else { b }); }
function via_str(a: string, j: i32): i32 { return slen(if (j > 1) { a } else { a + "z" }); }
function mkd(j: i32): dyn Shape { return Rect { w: j, h: 2, tag: "q" + "r" }; }
function main(): i32 {
    var s = "abc" + "def";
    var d1: dyn Shape = Square { side: 3 };
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        t = t + measure(if (j > 0) { d1 } else { mkd(j) });
        t = t + measure(match (j) { 0 => mkd(j), _ => d1 });
        t = t + via_param([j], mk(j), j > 1);
        t = t + via_str(s, j);
        t = t + slen({ var q = s; q });
        j = j + 1;
    }
    return (t + s.len() + d1.area()) % 101;
}
`

// A block tail or an arm that yields a string, struct or nested array is not
// released by its consumer: that release is one buffer dec, which would free
// the buffer and strand its elements. The AST lowering leaks these whole, which
// its pinned census holds; the semantic lowering releases them.
const condNonScalarElemSrc = `struct P { x: i32, y: i32 }
function tail(): string { return "xyz"; }
function slen(xs: string[]): i32 { return xs.len(); }
function pn(ps: P[]): i32 { return ps.len(); }
function main(): i32 {
    var s = "abc" + tail();
    var a = [s + "1", s + "2"];
    var ps = [P { x: 1, y: 2 }];
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 10) {
        t = t + ({ var q = [s + "a", s + "b"]; q }).len();
        t = t + ({ var q = [s + "c"]; q })[0].len();
        t = t + slen({ var q = [s + "d"]; q });
        t = t + ({ var q = [P { x: j, y: 1 }, P { x: 2, y: j }]; q }).len();
        t = t + ({ var q = [P { x: j, y: 3 }]; q })[0].y;
        t = t + pn({ var q = [P { x: j, y: 4 }]; q });
        t = t + ({ var q = [[j, 1], [2, j]]; q }).len();
        t = t + ({ var q = [[j, 3]]; q })[0][1];
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
    var t: i32 = 0;
    var j: i32 = 0;
    var a: P = P { x: 0, y: 0 };
    var b: P = P { x: 0, y: 0 };
    var c: dyn Shape = P { x: 0, y: 0 };
    while (j < 10) {
        var q = [P { x: 5, y: j + 100 }, P { x: 6, y: j + 1 }];
        var z1 = mkp(j);
        var z2 = mkp(j + 1);
        var z3 = mkp(j + 2);
        t = t + a.y + b.x + c.area() + z1.y + z2.y + z3.y + q[1].y;
        a = id(q[1]);
        b = idg(q[0]);
        c = todyn(q[1]);
        var d = id(q[0]);
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
    var p: P = P { xs: [i, i + 1], k: i };
    var o: Option[P] = Some(p);
    return (match (o) { Some(q) => q.k, None => 0 });
}
function live(i: i32): i32 {
    var p: P = P { xs: [i, i + 1], k: i };
    var o: Option[P] = Some(p);
    return (match (o) { Some(q) => q.k, None => 0 }) + p.xs[0];
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 10) { t = t + moved(r) + live(r); r = r + 1; }
    return t % 97;
}
`

// A struct array a value block declares ahead of a tail if or match keeps its
// element credit.
const condBlockTailMatchSrc = `struct P { x: i32, y: i32 }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 10) {
        var n = { var qa = [P { x: 5, y: j }]; match (j) { 0 => qa[0].y, _ => qa[0].x } };
        var m = { var qb = [P { x: 5, y: j }]; if (j > 3) { qb[0].x } else { qb[0].y } };
        t = t + n + m;
        j = j + 1;
    }
    return t % 101;
}
`

// Interpreter-confirmed answers. astCensus, when set, is the exact
// allocs/frees the AST lowering reports, which then need not balance.
var condReleaseCases = []struct {
	name      string
	src       string
	want      int
	balanced  bool
	astCensus [2]int64
}{
	{"array", condArrReleaseSrc, 86, true, [2]int64{}},
	{"string", condStrReleaseSrc, 19, true, [2]int64{}},
	{"struct", condStructReleaseSrc, 89, true, [2]int64{}},
	{"struct_array_locals", condStructArrLocalsSrc, 88, true, [2]int64{}},
	{"element_handout", condElemHandoutSrc, 75, true, [2]int64{}},
	{"alias_string", condAliasStrSrc, 34, true, [2]int64{}},
	{"alias_struct", condAliasStructSrc, 65, true, [2]int64{}},
	{"alias_dyn", condAliasDynSrc, 12, true, [2]int64{}},
	{"alias_outlives", condAliasOutlivesSrc, 95, true, [2]int64{}},
	{"bare_name_block", condBareNameBlockSrc, 81, true, [2]int64{}},
	{"not_owned", condNotOwnedSrc, 98, false, [2]int64{}},
	{"nonscalar_elements", condNonScalarElemSrc, 73, true, [2]int64{223, 1}},
	{"row_handout", condRowHandoutSrc, 59, true, [2]int64{}},
	{"option_match_return", condOptionMatchReturnSrc, 38, true, [2]int64{}},
	{"block_tail_match_credit", condBlockTailMatchSrc, 81, true, [2]int64{}},
}

func TestSelfHostConditionalValueReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range condReleaseCases {
		for _, lw := range vblockClosureBoth {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := cli.exitOf(t, tc.src, "x86-64-linux", "FERN_LEAKCHECK=1", lw.env)
				if exit != tc.want {
					t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				balanced := assertCondCensus(t, stderr, tc.balanced, tc.astCensus, lw.name)
				stderr, exit = cli.exitOf(t, tc.src, "x86-64-linux", "FERN_SANITIZE=1", lw.env)
				if exit != tc.want || forArrStructSanitizerFault(stderr, balanced) {
					t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer fault\n%s", exit, tc.want, stderr)
				}
			})
		}
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
		for _, lw := range vblockClosureBoth {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := cli.exitOf(t, tc.src, target, "FERN_LEAKCHECK=1", lw.env)
				if exit != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				assertCondCensus(t, stderr, tc.balanced, tc.astCensus, lw.name)
			})
		}
	}
}

// assertCondCensus requires a balanced census, the pinned AST census on the
// AST leg of a case that has one, or for a program that is allowed to leak, no
// more frees than allocations. It reports whether the leg had to balance.
func assertCondCensus(t *testing.T, stderr string, balanced bool, astCensus [2]int64, lowering string) bool {
	t.Helper()
	if lowering == "ast" && astCensus != [2]int64{} {
		assertRefusedCensus(t, stderr, astCensus)
		return false
	}
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
