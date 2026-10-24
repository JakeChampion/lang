package e2e

import (
	"testing"
)

var rcIndirectDispatchCorpus = []struct {
	name string
	src  string
}{
	{
		name: "two_destructuring_lambdas",
		src: `
function main(): i32 {
    let lam = ((x, y): (i32, i32)): i32 => { return x * 10 + y; };
    if (lam((3, 4)) != 34) { return 1; }
    let diff = ((p, q): (i32, i32)): i32 => { return p - q; };
    if (diff((9, 5)) != 4) { return 2; }
    return __rc_underflow_count();
}`,
	},
	{
		name: "lambda_arg_is_live_local",
		src: `
function main(): i32 {
    let lam = (p: (i32, i32)): i32 => { return p.0 * 10 + p.1; };
    let t = (3, 4);
    let a = lam(t);
    let b = lam(t);
    if (a + b != 68) { return 1; }
    if (t.0 + t.1 != 7) { return 2; }
    return __rc_underflow_count();
}`,
	},
	{
		name: "named_function_as_callback",
		src: `
function sum_pair(p: (i32, i32)): i32 { return p.0 + p.1; }
function apply(f: ((i32, i32)) => i32, t: (i32, i32)): i32 { return f(t); }
function main(): i32 {
    if (apply(sum_pair, (1, 2)) != 3) { return 1; }
    if (apply(sum_pair, (10, 20)) != 30) { return 2; }
    return __rc_underflow_count();
}`,
	},
	{
		name: "named_function_direct_and_indirect",
		src: `
function sum_pair(p: (i32, i32)): i32 { return p.0 + p.1; }
function apply(f: ((i32, i32)) => i32, t: (i32, i32)): i32 { return f(t); }
function main(): i32 {
    let t = (4, 5);
    if (sum_pair(t) != 9) { return 1; }
    if (apply(sum_pair, t) != 9) { return 2; }
    if (sum_pair((1, 2)) != 3) { return 3; }
    return __rc_underflow_count();
}`,
	},
	{
		name: "two_struct_param_lambdas",
		src: `
struct P { x: i32, y: i32 }
function main(): i32 {
    let f = (p: P): i32 => { return p.x * 10 + p.y; };
    if (f(P { x: 3, y: 4 }) != 34) { return 1; }
    let g = (q: P): i32 => { return q.x - q.y; };
    if (g(P { x: 9, y: 5 }) != 4) { return 2; }
    return __rc_underflow_count();
}`,
	},
	{
		name: "lambda_param_escapes_into_result",
		src: `
struct Box { t: (i32, i32) }
function build(f: ((i32, i32)) => Box): Box {
    let t = (3, 4);
    return f(t);
}
function main(): i32 {
    let mk = (p: (i32, i32)): Box => { return Box { t: p }; };
    let b = build(mk);
    if (b.t.0 * 10 + b.t.1 != 34) { return 1; }
    return __rc_underflow_count();
}`,
	},
	{
		name: "lambda_array_param",
		src: `
function main(): i32 {
    let f = (xs: i32[]): i32 => { return xs[0] + xs[1] + xs.len(); };
    let a = [1, 2];
    if (f(a) != 5) { return 1; }
    if (f([3, 4]) != 9) { return 2; }
    if (a[0] + a[1] != 3) { return 3; }
    return __rc_underflow_count();
}`,
	},
}

func runRcIndirectDispatchCorpus(t *testing.T, run func(t *testing.T, src string) int) {
	t.Helper()
	for _, c := range rcIndirectDispatchCorpus {
		t.Run(c.name, func(t *testing.T) {
			if code := run(t, c.src); code != 0 {
				t.Errorf("%s: got exit %d, want 0 (wrong value or rc over-release)", c.name, code)
			}
		})
	}
}

func TestX86_64RcIndirectDispatchOwned(t *testing.T) {
	runRcIndirectDispatchCorpus(t, func(t *testing.T, src string) int {
		_, code := compileAndRunX86_64FreeOn(t, src)
		return code
	})
}

func TestArm64RcIndirectDispatchOwned(t *testing.T) {
	runRcIndirectDispatchCorpus(t, func(t *testing.T, src string) int {
		_, code := compileAndRunArm64FreeOn(t, src)
		return code
	})
}

func TestWASMRcIndirectDispatchOwned(t *testing.T) {
	runRcIndirectDispatchCorpus(t, runWasm)
}

var rcIndirectDispatchDefaultCorpus = []struct {
	name string
	src  string
}{
	{
		name: "identity_return_lambda",
		src: `
function main(): i32 {
    let id = (p: (i32, i32)): (i32, i32) => { return p; };
    let t = (3, 4);
    let u = id(t);
    let j1 = (91, 92);
    let j2 = (93, 94);
    if (j1.0 + j2.0 != 184) { return 200; }
    if (u.0 * 10 + u.1 != 34) { return 100 + u.0; }
    if (t.0 * 10 + t.1 != 34) { return 150 + t.0; }
    return __rc_underflow_count();
}`,
	},
	{
		name: "named_pick_as_callback",
		src: `
function pick(p: (i32, i32), q: (i32, i32), c: boolean): (i32, i32) { if (c) { return p; } return q; }
function apply(f: ((i32,i32), (i32,i32), boolean) => (i32,i32)): i32 {
    let a = (3, 4);
    let b = (5, 6);
    let r = f(a, b, true);
    let j1 = (91, 92);
    if (j1.0 != 91) { return 200; }
    return r.0 * 10 + r.1;
}
function main(): i32 {
    if (apply(pick) != 34) { return 1; }
    return __rc_underflow_count();
}`,
	},
}

func runRcIndirectDispatchDefaultCorpus(t *testing.T, run func(t *testing.T, src string) int) {
	t.Helper()
	for _, c := range rcIndirectDispatchDefaultCorpus {
		t.Run(c.name, func(t *testing.T) {
			if code := run(t, c.src); code != 0 {
				t.Errorf("%s: got exit %d, want 0 (wrong value or rc over-release)", c.name, code)
			}
		})
	}
}

func TestX86_64RcIndirectDispatchDefault(t *testing.T) {
	runRcIndirectDispatchDefaultCorpus(t, func(t *testing.T, src string) int {
		_, code := compileAndRunX86_64FreeOn(t, src)
		return code
	})
}

func TestArm64RcIndirectDispatchDefault(t *testing.T) {
	runRcIndirectDispatchDefaultCorpus(t, func(t *testing.T, src string) int {
		_, code := compileAndRunArm64FreeOn(t, src)
		return code
	})
}

func TestWASMRcIndirectDispatchDefault(t *testing.T) {
	runRcIndirectDispatchDefaultCorpus(t, runWasm)
}
