package e2ecompiler

import "testing"

var explicitTypeArgsIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"scrutinee",
		`trait Mk { function make(n: i32): Self; } struct P { v: i32 } impl Mk for P { function make(n: i32): P { return P { v: n * 2 }; } } function build[T: Mk](n: i32): Result[T, string] { if (n < 0) { return Err("negative"); } return Ok(T.make(n)); } function main(): i32 { match (build[P](5)) { Ok(p) => { return p.v; }, Err(e) => { return 100; } } return 0; }`, 10},
	{"unannotated-var",
		`trait Mk { function make(n: i32): Self; } struct P { v: i32 } impl Mk for P { function make(n: i32): P { return P { v: n * 2 }; } } function build[T: Mk](n: i32): T { return T.make(n); } function main(): i32 { let p = build[P](7); return p.v; }`, 14},
	{"forwarded",
		`trait Mk { function make(n: i32): Self; } struct P { v: i32 } impl Mk for P { function make(n: i32): P { return P { v: n * 2 }; } } function decode[T: Mk](n: i32): Result[T, string] { if (n < 0) { return Err("negative"); } return Ok(T.make(n)); } function tryit[T: Mk](n: i32): Result[T, string] { let t: T = decode[T](n)?; return Ok(t); } function main(): i32 { match (tryit[P](3)) { Ok(p) => { return p.v + 1; }, Err(e) => { return 100; } } return 0; }`, 7},
	{"prim-direct",
		`trait Mk { function make(n: i32): Self; } impl Mk for i32 { function make(n: i32): i32 { return n + 1; } } function main(): i32 { return i32.make(41); }`, 42},
	{"prim-through-bound",
		`trait Mk { function make(n: i32): Self; } impl Mk for i32 { function make(n: i32): i32 { return n + 1; } } function build[T: Mk](n: i32): T { return T.make(n); } function main(): i32 { let a: i32 = build(20); let b: i32 = build[i32](20); return a + b; }`, 42},
	{"prim-result",
		`trait Mk { function make(n: i32): Result[Self, string]; } impl Mk for i32 { function make(n: i32): Result[Self, string] { if (n < 0) { return Err("neg"); } return Ok(n + 1); } } function decode[T: Mk](n: i32): Result[T, string] { return T.make(n); } function main(): i32 { let r: Result[i32, string] = decode(9); match (r) { Ok(x) => { return x; }, Err(e) => { return 100; } } return 0; }`, 10},
	{"nested-instantiation",
		`struct Q { v: i32 } struct W[T] { t: T } struct V[T] { u: T } function pass[T](x: T): T { return x; } function main(): i32 { let fns: ((i32) => i32)[] = [(n: i32) => n + 1, (n: i32) => n * 2]; let order: i32[] = [1, 0]; let a: W[Q] = pass[W[Q]](W { t: Q { v: 3 } }); let b: V[W[Q]] = pass[V[W[Q]]](V { u: a }); return a.t.v + b.u.t.v * 10 + fns[order[0]](10); }`, 53},
}

func TestSelfHostExplicitTypeArgsIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range explicitTypeArgsIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
