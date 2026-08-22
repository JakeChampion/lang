package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A function reached through a function VALUE — a lambda kept in a local, a
// named function handed over as a callback — is dispatched through a function
// pointer, so the call site has no callee name to hang a caller-side retain
// on. Its parameters therefore BORROW under every ownership model
// (addressTakenFuncs → paramVerdict), exactly as a `dyn Trait` vtable slot's
// do. Classify one owned instead and the callee decs at exit a reference
// nobody incremented: the caller's value is freed underneath it (#7307).
//
// Borrow inference reaches the same verdict from the escape facts, so the
// default configuration cannot see any of this — every case here runs with
// ast.BorrowInferEnabled OFF, which is the model borrow inference is an
// optimisation OF. Each program folds __rc_underflow_count() into a value
// check, so a non-zero exit is either a wrong answer or an over-release; on
// wasm the over-release also corrupts the freelist and aborts outright.
//
// The exposed surface is exactly the owned-by-default set: a pointer-shaped
// param whose type is string/array-FREE, i.e. a tuple or struct of scalars.
// Measured on the pre-fix compiler, an indirect call taking a scalar, a
// string, an array or a string-bearing struct reported no underflow on any
// backend — paramVerdict's first rung is isOwnedByDefaultType, so those never
// carry the inc/dec pair on either side.
var rcIndirectDispatchCorpus = []struct {
	name string
	src  string
}{
	{
		// The reported repro: two destructuring-param lambdas in one
		// function. Each destructuring param is a synthetic holder param
		// plus a leading Destructure, so the tuple flows into a frame that
		// would reclaim it.
		name: "two_destructuring_lambdas",
		src: `
function main(): i32 {
    var lam = function ((x, y): (i32, i32)): i32 { return x * 10 + y; };
    if (lam((3, 4)) != 34) { return 1; }
    var diff = function ((p, q): (i32, i32)): i32 { return p - q; };
    if (diff((9, 5)) != 4) { return 2; }
    return __rc_underflow_count();
}`,
	},
	{
		// A live local passed to a lambda, rather than a fresh temp: the
		// caller keeps its reference and reads the tuple after the call.
		name: "lambda_arg_is_live_local",
		src: `
function main(): i32 {
    var lam = function (p: (i32, i32)): i32 { return p.0 * 10 + p.1; };
    var t = (3, 4);
    var a = lam(t);
    var b = lam(t);
    if (a + b != 68) { return 1; }
    if (t.0 + t.1 != 7) { return 2; }
    return __rc_underflow_count();
}`,
	},
	{
		// A NAMED function taken as a value. closureconv leaves it a bare
		// Ident rather than wrapping it in a MakeClosure, so both forms
		// have to be recognised as address-taken.
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
		// The same function called BOTH directly and through a value. The
		// two sides read one ownership ladder, so the direct call must not
		// retain what the definition side no longer decs.
		name: "named_function_direct_and_indirect",
		src: `
function sum_pair(p: (i32, i32)): i32 { return p.0 + p.1; }
function apply(f: ((i32, i32)) => i32, t: (i32, i32)): i32 { return f(t); }
function main(): i32 {
    var t = (4, 5);
    if (sum_pair(t) != 9) { return 1; }
    if (apply(sum_pair, t) != 9) { return 2; }
    if (sum_pair((1, 2)) != 3) { return 3; }
    return __rc_underflow_count();
}`,
	},
	{
		// The other half of the owned-by-default set: a struct of scalars
		// rather than a tuple. Same ladder, so the pattern kind was never
		// what decided it.
		name: "two_struct_param_lambdas",
		src: `
struct P { x: i32, y: i32 }
function main(): i32 {
    var f = function (p: P): i32 { return p.x * 10 + p.y; };
    if (f(P { x: 3, y: 4 }) != 34) { return 1; }
    var g = function (q: P): i32 { return q.x - q.y; };
    if (g(P { x: 9, y: 5 }) != 4) { return 2; }
    return __rc_underflow_count();
}`,
	},
	{
		// The param ESCAPES the indirect callee — it is stored into a struct
		// the callee returns, and the caller's own reference dies first. A
		// borrowed param that escapes must still be retained by the store
		// that carries it out, which is what makes the unconditional borrow
		// safe here (the same reasoning vtable-dispatched methods rely on).
		name: "lambda_param_escapes_into_result",
		src: `
struct Box { t: (i32, i32) }
function build(f: ((i32, i32)) => Box): Box {
    var t = (3, 4);
    return f(t);
}
function main(): i32 {
    var mk = function (p: (i32, i32)): Box { return Box { t: p }; };
    var b = build(mk);
    if (b.t.0 * 10 + b.t.1 != 34) { return 1; }
    return __rc_underflow_count();
}`,
	},
	{
		// An array argument rather than a tuple: a reference-typed param
		// whose reclaim frees a buffer, reached indirectly.
		name: "lambda_array_param",
		src: `
function main(): i32 {
    var f = function (xs: i32[]): i32 { return xs[0] + xs[1] + xs.len(); };
    var a = [1, 2];
    if (f(a) != 5) { return 1; }
    if (f([3, 4]) != 9) { return 2; }
    if (a[0] + a[1] != 3) { return 3; }
    return __rc_underflow_count();
}`,
	},
}

func runRcIndirectDispatchCorpus(t *testing.T, run func(t *testing.T, src string) int) {
	t.Helper()
	prevFree := ast.RcFreeEnabled
	defer func() { ast.RcFreeEnabled = prevFree }()
	ast.RcFreeEnabled = true
	prevBorrow := ast.BorrowInferEnabled
	defer func() { ast.BorrowInferEnabled = prevBorrow }()
	ast.BorrowInferEnabled = false
	for _, c := range rcIndirectDispatchCorpus {
		t.Run(c.name, func(t *testing.T) {
			if code := run(t, c.src); code != 0 {
				t.Errorf("%s: got exit %d, want 0 (wrong value or rc over-release under the owned model)", c.name, code)
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
