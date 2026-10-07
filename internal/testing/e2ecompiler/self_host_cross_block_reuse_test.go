package e2ecompiler

import "testing"

// A donor dying in one or more blocks hands its box to the first construction
// of each later block entered only by way of them (ssarc.carried_pairs); a
// path that leaves those ways drops the donor instead. Each case returns
// a value the interpreter agrees on, runs under the sanitizer and the leak
// census, and pins how many boxes it allocates: one fewer than without the
// reuse where the pairing applies, the same where it must not. A constant
// payload reaches its variant through `id`, which hides the constant from the static-box plan, so the
// variant is allocated rather than placed as a static box.
func TestSelfHostCrossBlockReuse(t *testing.T) {
	boxedProbes(t)
	cli := buildSelfHostCLI(t)
	cases := []struct {
		name   string
		src    string
		want   int
		allocs int64
	}{
		// `a` dies at its match; both arms rejoin at `c`, which takes its box.
		{"dead-donor", `enum E { A(i32[]), B(i32[]) }
@noinline function id(xs: i32[]): i32[] { return xs; }
function f(): i32 { let a: E = A(id([1, 2])); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B(id([3, 4])); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } return t + v; }
function main(): i32 { return f(); }`, 12, 1},
		// A fresh array allocated after the reuse reads back intact, and the
		// over-release detector stays at zero.
		{"corruption-probe", `enum E { A(i32[]), B(i32[]) }
function f(n: i32): i32 { let a: E = A([n, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, n]); let fresh: i32[] = [11, 22, n]; let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } if (t + v != 9 || fresh[0] + fresh[1] + fresh[2] != 34) { return 90; } return __rc_underflow_count(); }
function main(): i32 { return f(1); }`, 0, 4},
		// `a` is read after `c` is built, so it is not dead where the arms
		// rejoin and both boxes are allocated.
		{"donor-live", `enum E { A(i32[]), B(i32[]) }
@noinline function id(xs: i32[]): i32[] { return xs; }
function f(): i32 { let a: E = A(id([1, 2])); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B(id([3, 4])); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } match (a) { A(w) => { v = v + w[1]; }, B(_) => {} } return t + v; }
function main(): i32 { return f(); }`, 14, 2},
		// One arm returns before `c`, so a box held across it would leak.
		{"early-return-path", `enum E { A(i32[]), B(i32[]) }
@noinline function id(xs: i32[]): i32[] { return xs; }
function f(n: i32): i32 { let a: E = A([n, 2]); match (a) { A(w) => { if (w[0] > 5) { return 1; } }, B(_) => {} } let c: E = B(id([3, 4])); match (c) { A(w) => { return w[0]; }, B(w) => { return w[0] + w[1]; } } return 0; }
function main(): i32 { return f(9) * 10 + f(1); }`, 17, 5},
		// `c` is built in a loop the donor's block is outside, so a box handed
		// to its first iteration would be built into again on the next.
		{"construction-in-loop", `enum E { A(i32[]), B(i32[]) }
@noinline function id(xs: i32[]): i32[] { return xs; }
function f(): i32 { let a: E = A(id([1, 2])); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let s: i32 = 0; let i: i32 = 0; while (i < 3) { let c: E = B([i, 4]); match (c) { A(w) => { s = s + w[0]; }, B(w) => { s = s + w[0] + w[1]; } } i = i + 1; } return t + s; }
function main(): i32 { return f(); }`, 20, 7},
		// A struct donor dying in an `if`'s condition block hands its box to
		// the struct built where the arms rejoin.
		{"record-across-if", `struct P { x: i32, y: i32 }
function f(n: i32): i32 { let a: P = P { x: n, y: 2 }; let t: i32 = 0; if (a.x > 3) { t = 1; } else { t = 2; } let b: P = P { x: n * 3, y: 4 }; return t + b.x + b.y; }
function main(): i32 { return f(5) + f(1); }`, 29, 2},
		// Two carries in one function, `a` to `b` and `b` to `c`: the second
		// route search runs over the marks the first one left.
		{"records-across-two-ifs", `struct P { x: i32, y: i32 }
function f(n: i32): i32 { let a: P = P { x: n, y: 2 }; let t: i32 = 0; if (a.x > 3) { t = 1; } else { t = 2; } let b: P = P { x: n * 3, y: 4 }; if (b.x > 6) { t = t + 10; } else { t = t + 20; } let c: P = P { x: n, y: 5 }; return t + c.x + c.y; }
function main(): i32 { return f(5) + f(1); }`, 49, 2},
		// `b` is built in an arm `a`'s block branches into; the path that
		// skips the arm drops `a` on its way out.
		{"if-arm-in-loop", `struct P { x: i32, y: i32 }
function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, y: i + 1 }; let s: i32 = a.x + a.y; if (i > 0) { let b: P = P { x: i, y: 3 }; sum = sum + b.x + b.y; } sum = sum + s; i = i + 1; } return sum; }`, 31, 4},
		// The same at scale: a box handed on or dropped once per iteration,
		// never both and never neither.
		{"if-arm-in-loop-churn", `struct P { x: i32, y: i32 }
function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { let a: P = P { x: i, y: i + 1 }; let s: i32 = a.x + a.y; if (i > 0) { let b: P = P { x: i, y: 3 }; sum = (sum + b.x + b.y) % 1000; } sum = (sum + s) % 1000; i = i + 1; } return sum % 100; }`, 97, 5000000},
		// The box `b` is built in escapes into `acc`, which holds it past the
		// iteration.
		{"if-arm-escaping-recipient", `struct P { x: i32, y: i32 }
function main(): i32 { let acc: P[] = []; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, y: i + 1 }; let s: i32 = a.x + a.y; if (i > 0) { acc = acc.append(P { x: s, y: 1 }); } i = i + 1; } let sum: i32 = 0; for p in acc { sum = sum + p.x + p.y; } return sum; }`, 18, 5},
		// A donor shared with `keep` when it is carried: the construction finds
		// it shared and allocates, and the skipping path only releases it.
		{"shared-donor", `struct P { x: i32, y: i32 }
function main(): i32 { let sum: i32 = 0; let keep: P[] = []; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, y: i + 1 }; if (i == 1) { keep = keep.append(a); } let s: i32 = a.x + a.y; if (i > 0) { let b: P = P { x: i, y: 3 }; sum = sum + b.x + b.y; } sum = sum + s; i = i + 1; } return sum + keep[0].x * 10; }`, 41, 6},
		// A donor dying before a branch serves the construction in each arm:
		// at most one arm runs, so the box is spent at most once.
		{"record-into-both-arms", `struct P { x: i32, y: i32 }
function f(n: i32): i32 { let a: P = P { x: n, y: 2 }; let t: i32 = a.x; let r: i32 = 0; if (n > 3) { let b: P = P { x: n * 3, y: 4 }; r = b.x + b.y; } else { let c: P = P { x: n * 5, y: 6 }; r = c.x + c.y; } return t + r; }
function main(): i32 { return f(5) + f(1); }`, 36, 2},
		// The ways to the two arms share the `else if` test, and the arm
		// that returns first drops the donor on its way out.
		{"else-if-arms", `struct P { x: i32, xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
function f(n: i32): i32 { let a: P = P { x: n, xs: id([n, 1]) }; let s: i32 = a.x + a.xs[1]; if (n > 5) { return s; } else if (n > 2) { let b: P = P { x: n * 3, xs: id([4]) }; return s + b.x + b.xs[0]; } else { let c: P = P { x: n * 5, xs: id([6, 7]) }; return s + c.x + c.xs[1]; } }
function main(): i32 { return f(9) + f(3) + f(1); }`, 41, 6},
		// Each iteration's donor is rebuilt in whichever arm runs, its
		// array field released before the new one is stored.
		{"both-arms-in-loop", `struct P { x: i32, xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, xs: id([1]) }; let s: i32 = a.x + a.xs[0]; if (i % 2 == 0) { let b: P = P { x: i, xs: id([10]) }; sum = sum + b.x + b.xs[0]; } else { let c: P = P { x: i, xs: id([20]) }; sum = sum + c.x + c.xs[0]; } sum = sum + s; i = i + 1; } return sum; }`, 76, 4},
		// The same at scale, with an arm that builds nothing.
		{"both-arms-churn", `struct P { x: i32, xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 1000000) { let a: P = P { x: i, xs: id([i, 1]) }; let s: i32 = a.x + a.xs[1]; if (i % 3 == 0) { let b: P = P { x: i, xs: id([10]) }; sum = (sum + b.x + b.xs[0]) % 1000; } else if (i % 3 == 1) { let c: P = P { x: i, xs: id([20, 3]) }; sum = (sum + c.x + c.xs[1]) % 1000; } sum = (sum + s) % 1000; i = i + 1; } return sum % 100; }`, 39, 2000000},
		// A donor `keep` shares in two iterations: the arm's construction
		// finds it shared and allocates.
		{"shared-donor-both-arms", `struct P { x: i32, xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
function main(): i32 { let keep: P[] = []; let sum: i32 = 0; let i: i32 = 0; while (i < 6) { let a: P = P { x: i, xs: id([i, 1]) }; if (i == 2 || i == 3) { keep = keep.append(a); } let s: i32 = a.x + a.xs[1]; if (i % 2 == 0) { let b: P = P { x: i, xs: id([10]) }; sum = sum + b.x + b.xs[0]; } else { let c: P = P { x: i, xs: id([20, 3]) }; sum = sum + c.x + c.xs[1]; } sum = sum + s; i = i + 1; } return sum + keep[0].xs[0] + keep[1].x; }`, 80, 15},
		// `d` dies in each arm of the match on its field; both arms hold it
		// for the record built after they rejoin.
		{"dying-in-both-match-arms", `enum St { On(i32), Off }
struct M { tag: i32, st: St }
@noinline function id(n: i32): i32 { return n; }
function f(n: i32): i32 { let d: M = M { tag: n, st: On(id(n)) }; let s: i32 = 0; match (d.st) { On(v) => { s = v + d.tag; }, Off => { s = d.tag; } } let e: M = M { tag: n, st: Off }; if (n > 2) { s = s + e.tag; } let b: M = M { tag: n + 1, st: On(id(3)) }; match (b.st) { On(v) => { s = s + v + b.tag; }, Off => {} } return s; }
function main(): i32 { return f(1) + f(5); }`, 31, 8},
		// One arm builds a record of the donor's count before the donor's
		// last read; the donor dies after it in both arms all the same.
		{"dying-in-both-if-arms", `struct P { x: i32, xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
function f(n: i32): i32 { let a: P = P { x: n, xs: id([n, 1]) }; let s: i32 = 0; if (n > 3) { s = a.x; } else { let q: P = P { x: 9, xs: id([n]) }; s = q.x + a.xs[1]; } let b: P = P { x: n * 3, xs: id([4]) }; return s + b.x + b.xs[0]; }
function main(): i32 { return f(5) + f(1); }`, 41, 6},
		// `a` dies in all three arms, and the one that returns holds nothing:
		// it has no edge to drop a held box on.
		{"returning-arm-holds-nothing", `struct P { x: i32, xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
function f(n: i32): i32 { let a: P = P { x: n, xs: id([n, 1]) }; let s: i32 = 0; if (n > 3) { s = a.x; } else if (n > 1) { s = a.xs[1]; return s; } else { s = a.xs[0] + 1; } let b: P = P { x: n * 3, xs: id([4]) }; return s + b.x + b.xs[0]; }
function main(): i32 { return f(5) + f(2) + f(0); }`, 30, 6},
		// The match arms hold boxes of different counts, so only one could
		// serve `m`, and the join is entered from the other: nothing is
		// carried.
		{"arms-of-different-counts", `enum S { One(i32), Two(i32, i32) }
struct M { tag: i32, xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
@noinline function mk(n: i32): S { if (n % 2 == 0) { return One(n); } return Two(n, 1); }
function f(n: i32): i32 { let s: S = mk(n); let t: i32 = 0; match (s) { One(v) => { t = v; }, Two(v, w) => { t = v + w; } } let m: M = M { tag: t, xs: id([n]) }; return m.tag + m.xs[0]; }
function main(): i32 { return f(2) + f(3); }`, 11, 6},
		// The filter's tail calls become a loop over the walked cell, which
		// dies in both arms of the match on it: one arm builds a cell, the
		// other loops on with the payload. Its edge back drops the held cell
		// before the loop's phi takes the payload.
		{"held-across-the-loop-edge", `enum L { C(i32, L), N(i32, L), E }
function build(n: i32): L { let acc: L = E; let i: i32 = 0; while (i < n) { if (i % 3 == 1) { acc = N(i, acc); } else { acc = C(i - 2, acc); } i = i + 1; } return acc; }
@noinline function drop_neg(xs: L): L { match (xs) { C(h, t) => { if (h < 0) { return drop_neg(t); } return C(h, drop_neg(t)); }, N(h, t) => { return drop_neg(t); }, E => { return E; } } }
function score(l: L): i32 { let acc: i32 = 0; let cur: L = l; let go: boolean = true; while (go) { match (cur) { C(h, t) => { acc = acc * 3 + h; cur = t; }, N(h, t) => { acc = acc * 5 + h; cur = t; }, E => { go = false; } } } return acc; }
function main(): i32 { let keep: L = build(8); let before: i32 = score(keep); let d: i32 = score(drop_neg(keep)); return (d * 7 + score(keep) - before) % 101; }`, 57, 12},
		// Dying in the match arms, served in one of two later arms, at scale.
		{"match-arms-churn", `enum St { On(i32[]), Off(i32[]) }
struct M { tag: i32, st: St }
@noinline function id(xs: i32[]): i32[] { return xs; }
function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 1000000) { let d: M = M { tag: i, st: On(id([i, 1])) }; let s: i32 = 0; match (d.st) { On(v) => { s = v[1] + d.tag; }, Off(v) => { s = d.tag; } } if (i % 3 > 0) { let b: M = M { tag: i, st: Off(id([3])) }; match (b.st) { On(v) => { sum = sum + 1; }, Off(v) => { sum = (sum + v[0] + b.tag) % 1000; } } } sum = (sum + s) % 1000; i = i + 1; } return sum % 100; }`, 65, 3666666},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				stderr, code := cli.exitOf(t, tc.src, target, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1")
				if code != tc.want {
					t.Fatalf("%s: exit %d, want %d\n%s", target, code, tc.want, stderr)
				}
				allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
				if allocs != frees || live != 0 {
					t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", target, allocs, frees, live)
				}
				if allocs != tc.allocs {
					t.Errorf("%s: allocs=%d, want %d", target, allocs, tc.allocs)
				}
			}
		})
	}
}
