package e2eselfhost

import (
	"testing"
)

// A closure box a call returns, stored straight into a container, is released
// with the container (#10437): a struct field, an array literal element, an
// `.append` argument and a tuple element, each written with the call in place
// rather than bound to a local first. A closure-returning call hands its caller
// a counted box, so the container takes that count over.
const closureCallIntoContainerSrc = `struct H { f: (i32) => i32, n: i32 }
function mk(b: i32): (i32) => i32 { return (x: i32) => x - b; }
function mkh(i: i32): H { return H { f: mk(i * 3), n: i }; }
function main(): i32 {
    var t: i32 = 0;
    var fns: ((i32) => i32)[] = [];
    var hs: H[] = [];
    var q: ((i32) => i32, i32) = (mk(0), 0);
    var i: i32 = 0;
    while (i < 6) {
        var h: H = H { f: mk(i + 1), n: i };
        var fs: ((i32) => i32)[] = [mk(i), mk(1)];
        var p: ((i32) => i32, i32) = (mk(i), i);
        fns = fns.append(mk(i * 2));
        hs = hs.append(mkh(i));
        q = (mk(i + 2), i);
        t = t + h.f(10) + h.n + fs[0](20) + fs[1](3) + p.0(20) + p.1 + q.0(9);
        i = i + 1;
    }
    for f in fns { t = t + f(20); }
    for g in hs { t = t + g.f(100) + g.n; }
    return (t + q.1) % 101;
}
`

// Interpreter-confirmed.
const closureCallIntoContainerWant = 74

// The same containers built from a closure LOCAL that stays live after each
// container is released: the loop rebinds h, fs and p while g is still called,
// and keep outlives them all. The struct literal retains the local, so a struct
// that outlives it — appended to hs, or returned by wrap — still holds a live
// box; before, it borrowed the local's box and read it after the local's
// release freed it.
const closureContainerSharedSrc = `struct H { f: (i32) => i32, n: i32 }
function mk(b: i32): (i32) => i32 { return (x: i32) => x - b; }
function wrap(i: i32): H { var g: (i32) => i32 = mk(i); return H { f: g, n: i }; }
function main(): i32 {
    var t: i32 = 0;
    var keep: (i32) => i32 = mk(100);
    var hs: H[] = [];
    var i: i32 = 0;
    while (i < 6) {
        var g: (i32) => i32 = mk(i + 1);
        var h: H = H { f: g, n: i };
        var fs: ((i32) => i32)[] = [g, mk(1)];
        var p: ((i32) => i32, i32) = (g, i);
        hs = hs.append(H { f: g, n: i });
        hs = hs.append(wrap(i));
        if (i == 3) { keep = g; }
        t = t + h.f(10) + fs[0](20) + p.0(30) + p.1;
        t = t + g(40);
        i = i + 1;
    }
    for x in hs { t = t + x.f(100) + x.n; }
    return (t + keep(0)) % 101;
}
`

// Interpreter-confirmed.
const closureContainerSharedWant = 4

// A function returning a closure it does not own — a parameter, a struct
// field, an array element, a for-loop variable — retains it for the caller,
// which releases the result like any other closure-returning call's. Before,
// the caller's release freed the box the parameter's owner still held. The
// `pass(keep)(i)` callee form releases each evaluation's box. The rows after
// this one cover more borrowed spellings: the return retains every value its
// frame does not provably own, so no borrowed spelling is listed anywhere.
const closureReturnBorrowedSrc = `struct H { f: (i32) => i32, n: i32 }
function mk(b: i32): (i32) => i32 { return (x: i32) => x - b; }
function pass(f: (i32) => i32): (i32) => i32 { return f; }
function fieldof(h: H): (i32) => i32 { return h.f; }
function first(fs: ((i32) => i32)[]): (i32) => i32 { return fs[0]; }
function loopret(fs: ((i32) => i32)[]): (i32) => i32 { for f in fs { return f; } return mk(0); }
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 3;
    var keep: (i32) => i32 = mk(3);
    var h: H = H { f: (x: i32) => x * k, n: 1 };
    var fs: ((i32) => i32)[] = [(x: i32) => x + k, (x: i32) => x + k + 1];
    var i: i32 = 0;
    while (i < 6) {
        var g: (i32) => i32 = pass(keep);
        var a: (i32) => i32 = fieldof(h);
        var b: (i32) => i32 = first(fs);
        var c: (i32) => i32 = loopret(fs);
        t = t + g(10) + pass(keep)(i) + a(i) + b(i) + c(i);
        i = i + 1;
    }
    return (t + keep(50) + h.f(1) + fs[1](1)) % 101;
}
`

// Interpreter-confirmed.
const closureReturnBorrowedWant = 3

// An array element read through a struct field, returned directly and through
// the `elem(g)(i)` callee form.
const closureReturnFieldArraySrc = `struct G { fs: ((i32) => i32)[], n: i32 }
function mk(b: i32): (i32) => i32 { return (x: i32) => x - b; }
function elem(g: G): (i32) => i32 { return g.fs[0]; }
function main(): i32 {
    var t: i32 = 0;
    var g: G = G { fs: [mk(1), mk(2)], n: 4 };
    var i: i32 = 0;
    while (i < 6) {
        var f: (i32) => i32 = elem(g);
        t = t + f(10 + i) + elem(g)(i);
        i = i + 1;
    }
    return (t + g.fs[0](5) + g.fs[1](7) + g.n) % 101;
}
`

// Interpreter-confirmed.
const closureReturnFieldArrayWant = 91

// A tuple element (#10593).
const closureReturnTupleElemSrc = `function mk(b: i32): (i32) => i32 { return (x: i32) => x - b; }
function tfirst(p: ((i32) => i32, i32)): (i32) => i32 { return p.0; }
function main(): i32 {
    var t: i32 = 0;
    var p: ((i32) => i32, i32) = (mk(2), 9);
    var i: i32 = 0;
    while (i < 6) {
        var f: (i32) => i32 = tfirst(p);
        t = t + f(10 + i) + tfirst(p)(i);
        i = i + 1;
    }
    return (t + p.0(5) + p.1) % 101;
}
`

// Interpreter-confirmed.
const closureReturnTupleElemWant = 78

// A value-position `if` over array elements and a `match` over parameters.
const closureReturnValueBranchSrc = `function mk(b: i32): (i32) => i32 { return (x: i32) => x - b; }
function pickif(fs: ((i32) => i32)[], c: boolean): (i32) => i32 { return (if (c) { fs[0] } else { fs[1] }); }
function pickmatch(f: (i32) => i32, g: (i32) => i32, c: i32): (i32) => i32 { return match (c) { 0 => f, _ => g }; }
function main(): i32 {
    var t: i32 = 0;
    var fs: ((i32) => i32)[] = [mk(1), mk(2)];
    var keep: (i32) => i32 = mk(3);
    var alt: (i32) => i32 = mk(4);
    var i: i32 = 0;
    while (i < 6) {
        var f: (i32) => i32 = pickif(fs, i % 2 == 0);
        var g: (i32) => i32 = pickmatch(keep, alt, i % 3);
        t = t + f(10 + i) + g(i) + pickif(fs, i > 2)(i) + pickmatch(keep, keep, i)(1);
        i = i + 1;
    }
    return (t + fs[0](5) + fs[1](7) + keep(2) + alt(1)) % 101;
}
`

// Interpreter-confirmed.
const closureReturnValueBranchWant = 58

// A tuple-pattern `match`, which yields its value through a local every arm
// stores into: borrowed parameters from one function, fresh boxes from another.
const closureReturnPatternMatchSrc = `function mk(b: i32): (i32) => i32 { return (x: i32) => x - b; }
function pickb(f: (i32) => i32, g: (i32) => i32, c: i32): (i32) => i32 {
    return match ((c, 0)) { (0, _) => f, _ => g };
}
function pickf(k: i32, c: i32): (i32) => i32 {
    return match ((c, 0)) { (0, _) => ((x: i32) => x + k), _ => mk(k) };
}
function main(): i32 {
    var t: i32 = 0;
    var a: (i32) => i32 = mk(1);
    var b: (i32) => i32 = mk(2);
    var i: i32 = 0;
    while (i < 6) {
        var f: (i32) => i32 = pickb(a, b, i % 2);
        var g: (i32) => i32 = pickf(i, i % 2);
        t = t + f(10 + i) + g(i) + pickb(a, b, i % 3)(1) + pickf(i, i % 3)(2);
        i = i + 1;
    }
    return (t + a(5) + b(7)) % 101;
}
`

// Interpreter-confirmed.
const closureReturnPatternMatchWant = 86

// The result of calling a closure VALUE, which is not trusted to be counted:
// `() => keep` hands back its capture without a count (#10592).
const closureReturnClosureCallSrc = `function mk(b: i32): (i32) => i32 { return (x: i32) => x - b; }
function viaclo(g: () => ((i32) => i32)): (i32) => i32 { return g(); }
function main(): i32 {
    var t: i32 = 0;
    var keep: (i32) => i32 = mk(4);
    var src: () => ((i32) => i32) = () => keep;
    var i: i32 = 0;
    while (i < 6) {
        var a: (i32) => i32 = viaclo(src);
        var b: (i32) => i32 = mk(0);
        t = t + a(i) + b(i);
        i = i + 1;
    }
    return (t + keep(1)) % 101;
}
`

// Interpreter-confirmed.
const closureReturnClosureCallWant = 3

// Values the returning frame owns, so the return adds no count: a fresh
// lambda, a module function's box, a closure-returning call, a closure local
// the frame moves out, and a closure-returning method.
const closureReturnOwnedSrc = `struct M { b: i32 }
function mk(b: i32): (i32) => i32 { return (x: i32) => x - b; }
function dbl(x: i32): i32 { return x * 2; }
function fresh(b: i32): (i32) => i32 { return (x: i32) => x + b; }
function named(): (i32) => i32 { return dbl; }
function viacall(b: i32): (i32) => i32 { return mk(b + 1); }
function vialocal(b: i32): (i32) => i32 { var g: (i32) => i32 = mk(b); return g; }
function (m: M) make(): (i32) => i32 { return (x: i32) => x * m.b; }
function viamethod(m: M): (i32) => i32 { return m.make(); }
function main(): i32 {
    var t: i32 = 0;
    var m: M = M { b: 3 };
    var i: i32 = 0;
    while (i < 6) {
        var a: (i32) => i32 = fresh(i);
        var b: (i32) => i32 = named();
        var c: (i32) => i32 = viacall(i);
        var d: (i32) => i32 = vialocal(i);
        var f: (i32) => i32 = viamethod(m);
        t = t + a(i) + b(i) + c(i) + d(i) + f(i) + fresh(i)(1) + viamethod(m)(2);
        i = i + 1;
    }
    return t % 101;
}
`

// Interpreter-confirmed.
const closureReturnOwnedWant = 55

// An `own` fn parameter is the returning frame's too (#10958).
const closureReturnOwnParamSrc = `function mk(b: i32): (i32) => i32 { return (x: i32) => x - b; }
function passown(own f: (i32) => i32): (i32) => i32 { return f; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var e: (i32) => i32 = passown(mk(i));
        t = t + e(i + 7) + passown(mk(i))(1);
        i = i + 1;
    }
    return t % 101;
}
`

// Interpreter-confirmed.
const closureReturnOwnParamWant = 33

// An `own` fn parameter kept in a container the callee returns, beside a fn
// parameter a closure captures (#10958).
const closureOwnParamKeptSrc = `function apply_int(f: (i32) => i32, n: i32): i32 { return f(n); }
function mk(b: i32): (i32) => i32 { return (x: i32) => x - b; }
function via_capture(f: (i32) => i32, n: i32): i32 { return apply_int((x: i32): i32 => { return f(x) + 1; }, n); }
function keep(own f: (i32) => i32): ((i32) => i32)[] {
    var fs: ((i32) => i32)[] = [];
    fs = fs.append(f);
    return fs;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var g: (i32) => i32 = mk(i);
        t = t + via_capture(g, 10) + via_capture(mk(1), i);
        t = t + via_capture(g, 3);
        var ks: ((i32) => i32)[] = keep(mk(i));
        t = t + ks[0](20);
        t = t + keep(mk(i + 2))[0](2);
        i = i + 1;
    }
    return t % 101;
}
`

// Interpreter-confirmed.
const closureOwnParamKeptWant = 64

var closureIntoContainerCases = []struct {
	name string
	src  string
	want int
}{
	{"call_into_container", closureCallIntoContainerSrc, closureCallIntoContainerWant},
	{"container_shared", closureContainerSharedSrc, closureContainerSharedWant},
	{"return_borrowed", closureReturnBorrowedSrc, closureReturnBorrowedWant},
	{"return_field_array", closureReturnFieldArraySrc, closureReturnFieldArrayWant},
	{"return_tuple_elem", closureReturnTupleElemSrc, closureReturnTupleElemWant},
	{"return_value_branch", closureReturnValueBranchSrc, closureReturnValueBranchWant},
	{"return_pattern_match", closureReturnPatternMatchSrc, closureReturnPatternMatchWant},
	{"return_closure_call", closureReturnClosureCallSrc, closureReturnClosureCallWant},
	{"return_owned", closureReturnOwnedSrc, closureReturnOwnedWant},
	{"return_own_param", closureReturnOwnParamSrc, closureReturnOwnParamWant},
	{"own_param_kept", closureOwnParamKeptSrc, closureOwnParamKeptWant},
}

func TestSelfHostClosureIntoContainerX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range closureIntoContainerCases {
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := cli.exitOf(t, tc.src, "x86-64-linux", "FERN_LEAKCHECK=1")
			if exit != tc.want {
				t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
			stderr, exit = cli.exitOf(t, tc.src, "x86-64-linux", "FERN_SANITIZE=1")
			if exit != tc.want || forArrStructSanitizerFault(stderr, true) {
				t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer fault\n%s", exit, tc.want, stderr)
			}
		})
	}
}

func TestSelfHostClosureIntoContainerArm64(t *testing.T) {
	checkClosureIntoContainer(t, "arm64-linux")
}

func TestSelfHostClosureIntoContainerWasm(t *testing.T) {
	checkClosureIntoContainer(t, "wasm32-wasi")
}

func checkClosureIntoContainer(t *testing.T, target string) {
	cli := buildSelfHostCLI(t)
	for _, tc := range closureIntoContainerCases {
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := cli.exitOf(t, tc.src, target, "FERN_LEAKCHECK=1")
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
