package e2ecompiler

import "testing"

// An integer value block arm joined with an unsuffixed float sibling settles at
// that float as a bare literal arm does (#12381): `if (c) { ({ 1 }) } else {
// 2.5 }` is an f64, so the block's lambda is too. Each program's comparison
// holds only when the arm really became a float.
var valueBlockFloatArmCases = []struct {
	name string
	src  string
	want int
}{
	{"block-arm", `function main(): i32 { let c: boolean = true; let x = if (c) { ({ 1 }) } else { 2.5 }; if (x / 2.0 == 0.5) { return 3; } return 4; }`, 3},
	{"block-arm-leading-statement", `function main(): i32 { let x = if (true) { ({ let q: i32 = 3; 1 }) } else { 2.5 }; if (x / 2.0 == 0.5) { return 5; } return 6; }`, 5},
	{"nested-block-arm", `function main(): i32 { let x = if (true) { ({ ({ 1 }) }) } else { 2.5 }; if (x / 2.0 == 0.5) { return 7; } return 8; }`, 7},
	{"block-arm-after-float", `function main(): i32 { let x = if (false) { 2.5 } else { ({ 1 }) }; if (x / 2.0 == 0.5) { return 9; } return 10; }`, 9},
	{"match-block-arm", `function main(): i32 { let k: i32 = 1; let x = match (k) { 1 => ({ 1 }), _ => 2.5 }; if (x / 2.0 == 0.5) { return 11; } return 12; }`, 11},
	{"block-arm-f32", `function main(): i32 { let x = if (true) { ({ 1 }) } else { 2.5f32 }; if (x / 2.0f32 == 0.5f32) { return 13; } return 14; }`, 13},
	{"block-arm-return", `function g(c: boolean): f64 { return if (c) { ({ 1 }) } else { 2.5 }; } function main(): i32 { if (g(true) / 2.0 == 0.5) { return 15; } return 16; }`, 15},
	{"block-return", `function g(): f64 { return ({ 1 }); } function main(): i32 { if (g() / 2.0 == 0.5) { return 17; } return 18; }`, 17},
	{"block-arm-argument", `function g(v: f64): f64 { return v / 2.0; } function main(): i32 { if (g(if (true) { ({ 1 }) } else { 2.5 }) == 0.5) { return 19; } return 20; }`, 19},
	// The same settle reaches tuple elements and enum payloads.
	{"tuple-elem", `function main(): i32 { let c: boolean = true; let t = if (c) { (1, 2) } else { (3.5, 4) }; if (t.0 / 2.0 == 0.5 && t.1 == 2) { return 21; } return 22; }`, 21},
	{"tuple-elem-after-float", `function main(): i32 { let c: boolean = false; let t = if (c) { (3.5, 4) } else { (1, 2) }; if (t.0 / 2.0 == 0.5) { return 23; } return 24; }`, 23},
	{"tuple-elem-typed-sibling", `function main(): i32 { let c: boolean = true; let n: i32 = 4; let t = if (c) { (1, n) } else { (3.5, 4) }; if (t.0 / 2.0 == 0.5 && t.1 == 4) { return 25; } return 26; }`, 25},
	{"nested-tuple-elem", `function main(): i32 { let c: boolean = true; let t = if (c) { ((1, 2), 3) } else { ((3.5, 4), 5) }; if (t.0.0 / 2.0 == 0.5) { return 27; } return 28; }`, 27},
	{"match-tuple-elem", `function main(): i32 { let k: i32 = 1; let t = match (k) { 1 => (1, 2), _ => (3.5, 4) }; if (t.0 / 2.0 == 0.5) { return 29; } return 30; }`, 29},
	{"block-tuple-arm", `function main(): i32 { let c: boolean = true; let t = if (c) { ({ (1, 2) }) } else { (3.5, 4) }; if (t.0 / 2.0 == 0.5) { return 31; } return 32; }`, 31},
	{"nested-if-tuple-elem", `function main(): i32 { let c: boolean = true; let t = if (c) { (if (true) { 1 } else { 2 }, 3) } else { (3.5, 4) }; if (t.0 / 2.0 == 0.5) { return 33; } return 34; }`, 33},
	{"tuple-elem-f32", `function main(): i32 { let c: boolean = true; let t = if (c) { (1, 2) } else { (3.5f32, 4) }; if (t.0 / 2.0f32 == 0.5f32) { return 35; } return 36; }`, 35},
	{"some-payload", `function main(): i32 { let c: boolean = true; let o = if (c) { Some(1) } else { Some(2.5) }; match (o) { Some(v) => { if (v / 2.0 == 0.5) { return 37; } }, None => {} } return 38; }`, 37},
	{"some-payload-after-float", `function main(): i32 { let c: boolean = false; let o = if (c) { Some(2.5) } else { Some(1) }; match (o) { Some(v) => { if (v / 2.0 == 0.5) { return 39; } }, None => {} } return 40; }`, 39},
	{"some-tuple-payload", `function main(): i32 { let c: boolean = true; let o = if (c) { Some((1, 2)) } else { Some((3.5, 4)) }; match (o) { Some(v) => { if (v.0 / 2.0 == 0.5) { return 41; } }, None => {} } return 42; }`, 41},
	{"ok-payload", `function main(): i32 { let c: boolean = true; let r = if (c) { Ok(1) } else { Ok(2.5) }; match (r) { Ok(v) => { if (v / 2.0 == 0.5) { return 43; } }, Err(e) => {} } return 44; }`, 43},
	{"err-payload", `function main(): i32 { let c: boolean = true; let r = if (c) { Err(1) } else { Err(2.5) }; match (r) { Ok(v) => {}, Err(e) => { if (e / 2.0 == 0.5) { return 45; } } } return 46; }`, 45},
	{"match-some-payload", `function main(): i32 { let k: i32 = 2; let o = match (k) { 1 => Some(2.5), 2 => Some(1), _ => None }; match (o) { Some(v) => { if (v / 2.0 == 0.5) { return 47; } }, None => {} } return 48; }`, 47},
	{"generic-enum-payload", `enum Bx[T] { Full(T), Empty } function main(): i32 { let c: boolean = true; let b = if (c) { Full(1) } else { Full(2.5) }; match (b) { Full(v) => { if (v / 2.0 == 0.5) { return 49; } }, Empty => {} } return 50; }`, 49},
	{"generic-enum-payload-after-float", `enum Bx[T] { Full(T), Empty } function main(): i32 { let c: boolean = false; let b = if (c) { Full(2.5) } else { Full(1) }; match (b) { Full(v) => { if (v / 2.0 == 0.5) { return 51; } }, Empty => {} } return 52; }`, 51},
	{"match-generic-enum-payload", `enum Bx[T] { Full(T), Empty } function main(): i32 { let k: i32 = 2; let b = match (k) { 1 => Full(2.5), 2 => Full(1), _ => Empty }; match (b) { Full(v) => { if (v / 2.0 == 0.5) { return 53; } }, Empty => {} } return 54; }`, 53},
	{"generic-enum-same-arms", `enum Bx[T] { Full(T), Empty } function main(): i32 { let c: boolean = true; let b = if (c) { Full(1) } else { Full(2) }; match (b) { Full(v) => { if (v == 1) { return 55; } }, Empty => {} } return 56; }`, 55},
	{"two-param-enum-payload", `enum Pr[A, B] { Both(A, B), Neither } function main(): i32 { let c: boolean = true; let p = if (c) { Both(1, 7) } else { Both(2.5, 8) }; match (p) { Both(x, y) => { if (x / 2.0 == 0.5 && y == 7) { return 57; } }, Neither => {} } return 58; }`, 57},
	{"nested-generic-enum-payload", `enum Bx[T] { Full(T), Empty } function main(): i32 { let c: boolean = true; let b = if (c) { Full(Full(1)) } else { Full(Full(2.5)) }; match (b) { Full(inner) => { match (inner) { Full(v) => { if (v / 2.0 == 0.5) { return 59; } }, Empty => {} } }, Empty => {} } return 60; }`, 59},
	{"generic-enum-some-payload", `enum Bx[T] { Full(T), Empty } function main(): i32 { let c: boolean = true; let b = if (c) { Full(Some(1)) } else { Full(Some(2.5)) }; match (b) { Full(o) => { match (o) { Some(v) => { if (v / 2.0 == 0.5) { return 61; } }, None => {} } }, Empty => {} } return 62; }`, 61},
	{"generic-enum-tuple-payload", `enum Bx[T] { Full(T), Empty } function main(): i32 { let c: boolean = true; let b = if (c) { Full((1, 2)) } else { Full((3.5, 4)) }; match (b) { Full(t) => { if (t.0 / 2.0 == 0.5 && t.1 == 2) { return 63; } }, Empty => {} } return 64; }`, 63},
	{"generic-enum-payload-return-argument", `enum Bx[T] { Full(T), Empty } function g(c: boolean): Bx[f64] { return if (c) { Full(1) } else { Full(2.5) }; } function h(b: Bx[f64]): f64 { match (b) { Full(v) => { return v; }, Empty => { return 0.0; } } } function main(): i32 { if (h(g(true)) / 2.0 == 0.5 && h(if (true) { Full(1) } else { Full(2.5) }) == 1.0) { return 65; } return 66; }`, 65},
	// An unsettled payload settles at a destination naming the enum's instance.
	{"generic-enum-payload-at-destination", `enum Bx[T] { Full(T), Empty } enum Pr[A, B] { Both(A, B), Neither } function g(): Bx[f64] { return Full(1); } function main(): i32 { let b: Bx[f64] = Full(1); let q: Pr[Option[f64], i64] = Both(Some(1), 2); match (b) { Full(v) => { match (g()) { Full(w) => { match (q) { Both(o, n) => { match (o) { Some(x) => { if (v / 2.0 == 0.5 && w / 2.0 == 0.5 && x / 2.0 == 0.5 && n == 2i64) { return 67; } }, None => {} } }, Neither => {} } }, Empty => {} } }, Empty => {} } return 68; }`, 67},
	// A value block whose arms settle at the destination is built at the
	// destination's instance, not its arms' own join (Bx[i32]).
	{"generic-enum-block-at-i64", `enum Bx[T] { Full(T), Empty } function main(): i32 { let c: boolean = true; let b: Bx[i64] = if (c) { Full(1) } else { Full(2) }; match (b) { Full(v) => { if (v == 1) { return 69; } }, Empty => {} } return 70; }`, 69},
	{"generic-enum-block-at-u8", `enum Bx[T] { Full(T), Empty } function main(): i32 { let c: boolean = false; let b: Bx[u8] = if (c) { Full(1) } else { Full(200) }; match (b) { Full(v) => { if (v == 200) { return 71; } }, Empty => {} } return 72; }`, 71},
	{"generic-enum-block-at-f64", `enum Bx[T] { Full(T), Empty } function main(): i32 { let c: boolean = true; let b: Bx[f64] = if (c) { Full(1) } else { Full(2) }; match (b) { Full(v) => { if (v / 2.0 == 0.5) { return 73; } }, Empty => {} } return 74; }`, 73},
	{"generic-enum-block-return", `enum Bx[T] { Full(T), Empty } function g(c: boolean): Bx[i64] { return if (c) { Full(1) } else { Full(2) }; } function main(): i32 { match (g(true)) { Full(v) => { if (v == 1) { return 75; } }, Empty => {} } return 76; }`, 75},
	{"generic-enum-block-argument", `enum Bx[T] { Full(T), Empty } function h(b: Bx[i64]): i64 { match (b) { Full(v) => { return v; }, Empty => { return 0; } } } function main(): i32 { let c: boolean = false; if (h(if (c) { Full(1) } else { Full(2) }) == 2) { return 77; } return 78; }`, 77},
	{"generic-enum-block-assign", `enum Bx[T] { Full(T), Empty } function main(): i32 { let c: boolean = true; let b: Bx[i64] = Empty; b = if (c) { Full(1) } else { Full(2) }; match (b) { Full(v) => { if (v == 1) { return 79; } }, Empty => {} } return 80; }`, 79},
	{"generic-enum-match-block-at-i64", `enum Bx[T] { Full(T), Empty } function main(): i32 { let k: i32 = 2; let b: Bx[i64] = match (k) { 1 => Full(1), 2 => Full(2), _ => Empty }; match (b) { Full(v) => { if (v == 2) { return 81; } }, Empty => {} } return 82; }`, 81},
	{"generic-enum-if-in-match-block-at-u8", `enum Bx[T] { Full(T), Empty } function main(): i32 { let k: i32 = 1; let c: boolean = false; let b: Bx[u8] = match (k) { 1 => if (c) { Full(1) } else { Full(200) }, _ => Empty }; match (b) { Full(v) => { if (v == 200) { return 83; } }, Empty => {} } return 84; }`, 83},
	{"two-param-enum-block-at-destination", `enum Pr[A, B] { Both(A, B), Neither } function main(): i32 { let c: boolean = true; let p: Pr[i64, f64] = if (c) { Both(1, 2) } else { Both(3, 4) }; match (p) { Both(a, b) => { if (a == 1 && b / 4.0 == 0.5) { return 85; } }, Neither => {} } return 86; }`, 85},
	{"some-generic-enum-block-at-destination", `enum Bx[T] { Full(T), Empty } function main(): i32 { let c: boolean = true; let o: Option[Bx[i64]] = if (c) { Some(Full(1)) } else { Some(Full(2)) }; match (o) { Some(b) => { match (b) { Full(v) => { if (v == 1) { return 87; } }, Empty => {} } }, None => {} } return 88; }`, 87},
	{"generic-enum-block-in-some-at-destination", `enum Bx[T] { Full(T), Empty } function main(): i32 { let c: boolean = true; let o: Option[Bx[i64]] = Some(if (c) { Full(1) } else { Full(2) }); match (o) { Some(b) => { match (b) { Full(v) => { if (v == 1) { return 89; } }, Empty => {} } }, None => {} } return 90; }`, 89},
	{"generic-enum-block-field-and-element", `enum Bx[T] { Full(T), Empty } struct S { b: Bx[i64] } function main(): i32 { let c: boolean = false; let s: S = S { b: if (c) { Full(1) } else { Full(2) } }; let xs: Bx[i64][] = [if (c) { Full(3) } else { Full(4) }, Empty]; match (s.b) { Full(v) => { match (xs[0]) { Full(w) => { if (v == 2 && w == 4) { return 91; } }, Empty => {} } }, Empty => {} } return 92; }`, 91},
	// An array literal arm's unsettled elements settle at a typed sibling's
	// element type, with or without a destination.
	{"array-arm-literal-at-u64", `function f(c: boolean, big: i32): u64 { let w: u64[] = if (c) { [big as u64] } else { [1] }; let v = if (c) { [1, 2] } else { [big as u64] }; return w[0] + v[0]; } function main(): i32 { if (f(false, 3) == 4 && f(true, 7) == 8) { return 93; } return 94; }`, 93},
	{"array-arm-literal-match-at-i64", `function f(k: i32, big: i32): i64 { let w = match (k) { 0 => [-(big as i64)], _ => [2] }; return w[0]; } function main(): i32 { if (f(0, 3) == -3 && f(1, 3) == 2) { return 95; } return 96; }`, 95},
	{"array-arm-literal-at-float", `function f(c: boolean): f64 { let v = if (c) { [1.5] } else { [1] }; let u = if (c) { [[1]] } else { [[2.5]] }; return v[0] + u[0][0]; } function main(): i32 { if (f(false) == 3.5 && f(true) == 2.5) { return 97; } return 98; }`, 97},
	{"array-arm-enum-payload-at-float", `function f(c: boolean): f64 { let a = if (c) { [Some(1)] } else { [Some(2.5)] }; match (a[0]) { Some(v) => { return v; }, None => { return 0.0; } } } function main(): i32 { if (f(true) == 1.0 && f(false) == 2.5) { return 99; } return 100; }`, 99},
	// Two view arms join as a view.
	{"view-arms-stay-views", `function f(c: boolean, a: i64[]): i64 { let w = if (c) { a[:] } else { a[1:] }; let u = if (c) { [a[0]] } else { [7] }; return w[0] + u[0]; } function main(): i32 { let a: i64[] = [3, 4]; if (f(false, a) == 11 && f(true, a) == 6) { return 101; } return 102; }`, 101},
}

// TestSelfHostValueBlockFloatArmX86_64 runs each case as a self-host x86-64
// binary built under FERN_STRICT_IR.
func TestSelfHostValueBlockFloatArmX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range valueBlockFloatArmCases {
		t.Run(tc.name, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, tc.src, "x86-64-linux", "FERN_STRICT_IR=1"); code != tc.want {
				t.Errorf("x86-64 binary exited %d, want %d\n%s", code, tc.want, stderr)
			}
		})
	}
}
