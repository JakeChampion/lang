package e2ecompiler

import "testing"

var assocFnIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"basic",
		`trait Mk { function make(a: i32, b: i32): Self; } struct Pt { x: i32, y: i32 } impl Mk for Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } } function main(): i32 { let p: Pt = Pt.make(3, 4); return p.x + p.y; }`, 7},
	{"chained",
		`trait Mk { function make(a: i32, b: i32): Self; } struct Pt { x: i32, y: i32 } impl Mk for Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } } function main(): i32 { return Pt.make(3, 4).x + Pt.make(10, 20).y; }`, 23},
	{"inferred-binding",
		`trait Mk { function make(a: i32, b: i32): Self; } struct Pt { x: i32, y: i32 } impl Mk for Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } } function main(): i32 { let p = Pt.make(5, 6); return p.x + p.y; }`, 11},
	{"zero-arg",
		`trait Orig { function origin(): Self; } struct Pt { x: i32, y: i32 } impl Orig for Pt { function origin(): Pt { return Pt { x: 0, y: 0 }; } } function main(): i32 { let p: Pt = Pt.origin(); return p.x + p.y + 9; }`, 9},
	{"multi-assoc",
		`trait Mk2 { function make(a: i32, b: i32): Self; function scaled(a: i32): Self; } struct Pt { x: i32, y: i32 } impl Mk2 for Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } function scaled(a: i32): Pt { return Pt { x: a, y: a + a }; } } function main(): i32 { let p: Pt = Pt.make(2, 3); let q: Pt = Pt.scaled(5); return p.x + p.y + q.x + q.y; }`, 20},
	{"enum-tag",
		`trait Mk { function tag(n: i32): Self; } enum E { A(i32), B } impl Mk for E { function tag(n: i32): E { if (n > 0) { return A(n); } return B; } } function val(e: E): i32 { match (e) { A(n) => { return n; }, B => { return 99; } } return 0; } function main(): i32 { let x: E = E.tag(5); let y: E = E.tag(0); return val(x) + val(y); }`, 104},
	{"enum-inline",
		`trait Mk { function tag(n: i32): Self; } enum E { A(i32), B } impl Mk for E { function tag(n: i32): E { if (n > 0) { return A(n); } return B; } } function val(e: E): i32 { match (e) { A(n) => { return n; }, B => { return 99; } } return 0; } function main(): i32 { return val(E.tag(7)); }`, 7},
	{"enum-zero-arg",
		`trait Def { function def(): Self; } enum E { A(i32), B } impl Def for E { function def(): E { return B; } } function val(e: E): i32 { match (e) { A(n) => { return n; }, B => { return 42; } } return 0; } function main(): i32 { let x: E = E.def(); return val(x); }`, 42},
	{"enum-str-payload",
		`trait Mk { function of(s: string): Self; } enum E { Tag(string), Nil } impl Mk for E { function of(s: string): E { return Tag(s); } } function val(e: E): i32 { match (e) { Tag(w) => { return w.len(); }, Nil => { return 0; } } return 0; } function main(): i32 { let x: E = E.of("hello"); return val(x); }`, 5},
	{"free-fn-same-name",
		`trait Mk { function one(): Self; } struct Pt { x: i32 } impl Mk for Pt { function one(): Pt { return Pt { x: 7 }; } } function one(x: i32): i32 { return x + 1; } function main(): i32 { return one(41) + Pt.one().x; }`, 49},
	{"enum-inferred",
		`trait Mk { function tag(n: i32): Self; } enum E { A(i32), B } impl Mk for E { function tag(n: i32): E { if (n > 0) { return A(n); } return B; } } function val(e: E): i32 { match (e) { A(n) => { return n; }, B => { return 99; } } return 0; } function main(): i32 { let x = E.tag(3); return val(x); }`, 3},
}

func TestSelfHostAssocFnIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range assocFnIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
