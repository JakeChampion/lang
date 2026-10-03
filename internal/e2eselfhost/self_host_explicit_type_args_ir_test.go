package e2eselfhost

import "testing"

// explicitTypeArgsIRCases pin two shapes the self-host monomorphiser and
// lowering missed on the way to `@derive(json.FromJson)` (#9854), on both
// backends, with exit codes as the oracle.
//
// A call's written type arguments (`f[T](x)`) bind its instantiation
// before the arguments and the destination are consulted: a type
// parameter that appears only in the result, or one forwarded from the
// caller's own parameter inside a generic body, binds from nothing else.
// The monomorphiser inferred from the arguments alone, so `f[P](5)` as a
// match scrutinee stayed the template and a clone's `g[T](n)` named an
// instantiation nothing produced.
//
// An associated function of a primitive's impl (`i32.make(n)`, and
// `T.make(n)` once `T = i32`) is an associated call like a struct's: the
// lowering knew a struct by its declaration and an enum by its registered
// return type, and read the primitive's name as a function value.
var explicitTypeArgsIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// A written type argument binds a result-only type parameter: the call
	// is a match scrutinee, so no annotation could bind it. 5 * 2 = 10.
	{"scrutinee",
		`trait Mk { function make(n: i32): Self; } struct P { v: i32 } impl Mk for P { function make(n: i32): P { return P { v: n * 2 }; } } function build[T: Mk](n: i32): Result[T, string] { if (n < 0) { return Err("negative"); } return Ok(T.make(n)); } function main(): i32 { match (build[P](5)) { Ok(p) => { return p.v; }, Err(e) => { return 100; } } return 0; }`, 10},
	// An unannotated binding of the same call. 7 * 2 = 14.
	{"unannotated-var",
		`trait Mk { function make(n: i32): Self; } struct P { v: i32 } impl Mk for P { function make(n: i32): P { return P { v: n * 2 }; } } function build[T: Mk](n: i32): T { return T.make(n); } function main(): i32 { let p = build[P](7); return p.v; }`, 14},
	// A generic body forwards its own parameter explicitly, through a
	// Result and the try operator. (3 * 2) + 1 = 7.
	{"forwarded",
		`trait Mk { function make(n: i32): Self; } struct P { v: i32 } impl Mk for P { function make(n: i32): P { return P { v: n * 2 }; } } function decode[T: Mk](n: i32): Result[T, string] { if (n < 0) { return Err("negative"); } return Ok(T.make(n)); } function tryit[T: Mk](n: i32): Result[T, string] { let t: T = decode[T](n)?; return Ok(t); } function main(): i32 { match (tryit[P](3)) { Ok(p) => { return p.v + 1; }, Err(e) => { return 100; } } return 0; }`, 7},
	// A primitive's impl supplies an associated function, called on the
	// type directly. 41 + 1 = 42.
	{"prim-direct",
		`trait Mk { function make(n: i32): Self; } impl Mk for i32 { function make(n: i32): i32 { return n + 1; } } function main(): i32 { return i32.make(41); }`, 42},
	// The same through a bound, the instantiation written and inferred
	// from the destination. (20 + 1) + (20 + 1) = 42.
	{"prim-through-bound",
		`trait Mk { function make(n: i32): Self; } impl Mk for i32 { function make(n: i32): i32 { return n + 1; } } function build[T: Mk](n: i32): T { return T.make(n); } function main(): i32 { let a: i32 = build(20); let b: i32 = build[i32](20); return a + b; }`, 42},
	// A primitive's associated function answering a Result of Self,
	// consumed by a match. 9 + 1 = 10.
	{"prim-result",
		`trait Mk { function make(n: i32): Result[Self, string]; } impl Mk for i32 { function make(n: i32): Result[Self, string] { if (n < 0) { return Err("neg"); } return Ok(n + 1); } } function decode[T: Mk](n: i32): Result[T, string] { return T.make(n); } function main(): i32 { let r: Result[i32, string] = decode(9); match (r) { Ok(x) => { return x; }, Err(e) => { return 100; } } return 0; }`, 10},
	// A written type argument that is itself an instantiation parses as an
	// index and is retagged as a type (#10711); an index call of a value
	// array beside it stays one. 3 + 30 + 20 = 53.
	{"nested-instantiation",
		`struct Q { v: i32 } struct W[T] { t: T } struct V[T] { u: T } function pass[T](x: T): T { return x; } function main(): i32 { let fns: ((i32) => i32)[] = [(n: i32) => n + 1, (n: i32) => n * 2]; let order: i32[] = [1, 0]; let a: W[Q] = pass[W[Q]](W { t: Q { v: 3 } }); let b: V[W[Q]] = pass[V[W[Q]]](V { u: a }); return a.t.v + b.u.t.v * 10 + fns[order[0]](10); }`, 53},
}

// TestSelfHostExplicitTypeArgsIR compiles each case with the self-host CLI
// for x86-64 and wasm and checks the exit code.
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
