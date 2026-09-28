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
// `pass(keep)(i)` callee form releases each evaluation's box.
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

var closureIntoContainerCases = []struct {
	name string
	src  string
	want int
}{
	{"call_into_container", closureCallIntoContainerSrc, closureCallIntoContainerWant},
	{"container_shared", closureContainerSharedSrc, closureContainerSharedWant},
	{"return_borrowed", closureReturnBorrowedSrc, closureReturnBorrowedWant},
}

func TestSelfHostClosureIntoContainerX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range closureIntoContainerCases {
		for _, lw := range vblockClosureBoth {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := cli.exitOf(t, tc.src, "x86-64-linux", "FERN_LEAKCHECK=1", lw.env)
				if exit != tc.want {
					t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				assertBalancedCensus(t, stderr)
				stderr, exit = cli.exitOf(t, tc.src, "x86-64-linux", "FERN_SANITIZE=1", lw.env)
				if exit != tc.want || forArrStructSanitizerFault(stderr, true) {
					t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer fault\n%s", exit, tc.want, stderr)
				}
			})
		}
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
		for _, lw := range vblockClosureBoth {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := cli.exitOf(t, tc.src, target, "FERN_LEAKCHECK=1", lw.env)
				if exit != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
