package e2eselfhost

import "testing"

// A donor dying in one block hands its box to the first construction of a
// later block entered only by way of it (ssarc.carried_pairs); a path that
// leaves that way drops the donor instead. Each case returns
// a value the interpreter agrees on, runs under the sanitizer and the leak
// census, and pins how many boxes it allocates: one fewer than without the
// reuse where the pairing applies, the same where it must not.
func TestSelfHostCrossBlockReuse(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cases := []struct {
		name   string
		src    string
		want   int
		allocs int64
	}{
		// `a` dies at its match; both arms rejoin at `c`, which takes its box.
		{"dead-donor", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, 4]); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } return t + v; }
function main(): i32 { return f(); }`, 12, 1},
		// A fresh array allocated after the reuse reads back intact, and the
		// over-release detector stays at zero.
		{"corruption-probe", `enum E { A(i32[]), B(i32[]) }
function f(n: i32): i32 { let a: E = A([n, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, n]); let fresh: i32[] = [11, 22, n]; let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } if (t + v != 9 || fresh[0] + fresh[1] + fresh[2] != 34) { return 90; } return __rc_underflow_count(); }
function main(): i32 { return f(1); }`, 0, 4},
		// `a` is read after `c` is built, so it is not dead where the arms
		// rejoin and both boxes are allocated.
		{"donor-live", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, 4]); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } match (a) { A(w) => { v = v + w[1]; }, B(_) => {} } return t + v; }
function main(): i32 { return f(); }`, 14, 2},
		// One arm returns before `c`, so a box held across it would leak.
		{"early-return-path", `enum E { A(i32[]), B(i32[]) }
function f(n: i32): i32 { let a: E = A([n, 2]); match (a) { A(w) => { if (w[0] > 5) { return 1; } }, B(_) => {} } let c: E = B([3, 4]); match (c) { A(w) => { return w[0]; }, B(w) => { return w[0] + w[1]; } } return 0; }
function main(): i32 { return f(9) * 10 + f(1); }`, 17, 5},
		// `c` is built in a loop the donor's block is outside, so a box handed
		// to its first iteration would be built into again on the next.
		{"construction-in-loop", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let s: i32 = 0; let i: i32 = 0; while (i < 3) { let c: E = B([i, 4]); match (c) { A(w) => { s = s + w[0]; }, B(w) => { s = s + w[0] + w[1]; } } i = i + 1; } return t + s; }
function main(): i32 { return f(); }`, 20, 7},
		// A struct donor dying in an `if`'s condition block hands its box to
		// the struct built where the arms rejoin.
		{"record-across-if", `struct P { x: i32, y: i32 }
function f(n: i32): i32 { let a: P = P { x: n, y: 2 }; let t: i32 = 0; if (a.x > 3) { t = 1; } else { t = 2; } let b: P = P { x: n * 3, y: 4 }; return t + b.x + b.y; }
function main(): i32 { return f(5) + f(1); }`, 29, 2},
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
