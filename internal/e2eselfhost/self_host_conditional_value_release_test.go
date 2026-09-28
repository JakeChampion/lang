package e2eselfhost

import "testing"

// A value-position if, match or block handed straight to a consumer that does
// not bind it — a borrowing call's argument, a `.len()` receiver, an index or
// field read — is released there when it is owned whichever arm ran (#10438):
// an array local its arm retained, or a fresh value. The AST lowering left it
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

// Arms that yield a string, struct or dyn local are not retained, and a
// parameter yielded by a conditional reads as escaping, so none of these is
// provably owned: each has to leak rather than be released under its source.
const condNotOwnedSrc = `struct P { x: i32, y: i32 }
trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
struct Rect { w: i32, h: i32, tag: string }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h + self.tag.len(); } }
function measure(d: dyn Shape): i32 { return d.area(); }
function slen(s: string): i32 { return s.len(); }
function px(p: P): i32 { return p.x; }
function sum(xs: i32[]): i32 { var t: i32 = 0; for x in xs { t = t + x; } return t; }
function mk(j: i32): i32[] { return [j, j, j]; }
function via_param(a: i32[], b: i32[], c: boolean): i32 { return sum(if (c) { a } else { b }); }
function mkd(j: i32): dyn Shape { return Rect { w: j, h: 2, tag: "q" + "r" }; }
function main(): i32 {
    var s = "abc" + "def";
    var p = P { x: 9, y: 2 };
    var d1: dyn Shape = Square { side: 3 };
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        t = t + slen(if (j > 1) { s } else { s + "x" });
        t = t + slen(match (j) { 0 => s, _ => s + "y" });
        t = t + slen({ var q = s; q });
        t = t + (if (j > 1) { s } else { s + "x" }).len();
        t = t + px(if (j > 1) { p } else { P { x: 3, y: j } });
        t = t + px(match (j) { 0 => p, _ => P { x: 7, y: j } });
        t = t + measure(if (j > 0) { d1 } else { mkd(j) });
        t = t + measure(match (j) { 0 => mkd(j), _ => d1 });
        t = t + via_param([j], mk(j), j > 1);
        j = j + 1;
    }
    return (t + s.len() + p.x + d1.area()) % 101;
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
	{"not_owned", condNotOwnedSrc, 84, false},
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
				assertCondCensus(t, stderr, tc.balanced)
				stderr, exit = cli.exitOf(t, tc.src, "x86-64-linux", "FERN_SANITIZE=1", lw.env)
				if exit != tc.want || forArrStructSanitizerFault(stderr, tc.balanced) {
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
				assertCondCensus(t, stderr, tc.balanced)
			})
		}
	}
}

// assertCondCensus requires a balanced census, or for a program that is
// allowed to leak, no more frees than allocations.
func assertCondCensus(t *testing.T, stderr string, balanced bool) {
	t.Helper()
	if balanced {
		assertBalancedCensus(t, stderr)
		return
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil {
		t.Fatalf("no leakcheck summary: %v\n%s", err, stderr)
	}
	if frees > allocs {
		t.Errorf("allocs=%d frees=%d — more released than allocated", allocs, frees)
	}
}
