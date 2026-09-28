package e2eselfhost

import (
	"testing"
)

// A closure held in a variant payload is released with the variant (#9841).
// The enum release admission (enum_field_rc_droppable) takes an fn payload,
// the construction counts it, and the release gives the count back with one
// rc-guarded dec of the env box. Each program runs ten rounds and returns 99
// if __rc_underflow_count() moved.

// The payload built from a lambda, a closure local and a closure-returning
// call; matched with the payload ignored, bound and called, and never matched.
const closureVariantBuiltSrc = `enum E { A(i32, (i32) => i32), B }
function mk(k: i32): (i32) => i32 { return (x: i32): i32 => x + k; }
function round(i: i32): i32 {
    var a: i32 = 0;
    var v1: E = A(i, (x: i32): i32 => x * 2 + i);
    match (v1) { A(n, _) => { a = a + n; }, B => { a = a + 1; } }
    var v2: E = A(i, (x: i32): i32 => x + i);
    match (v2) { A(n, f) => { a = a + f(n); }, B => { a = a + 1; } }
    var g: (i32) => i32 = (x: i32): i32 => x * 3 + i;
    var v3: E = A(i, g);
    match (v3) { A(n, f) => { a = a + f(n); }, B => { a = a + 1; } }
    var v4: E = A(i, mk(i));
    match (v4) { A(n, f) => { a = a + f(n); }, B => { a = a + 1; } }
    var v5: E = E.A(i + 1, mk(2));
    var v6: E = B;
    match (v6) { A(n, f) => { a = a + f(n); }, B => { a = a + 5; } }
    return a + g(1);
}
` + closureVariantMain

// Enums returned from functions: a lambda payload, a call payload behind a
// branch that also returns a unit variant, a parameter stored as the payload,
// and results matched, left unused, bound and lent, or lent straight from the
// call.
const closureVariantReturnedSrc = `enum E { A(i32, (i32) => i32), B }
function mk(k: i32): (i32) => i32 { return (x: i32): i32 => x + k; }
function lam(i: i32): E { return A(i, (x: i32): i32 => x * i); }
function viacall(i: i32): E {
    if (i % 3 == 0) { return B; }
    return A(i, mk(i));
}
function wrap(n: i32, f: (i32) => i32): E { return A(n, f); }
function use_e(e: E): i32 {
    match (e) { A(n, f) => { return f(n); }, B => { return 7; } }
    return 0;
}
function round(i: i32): i32 {
    var a: i32 = 0;
    var v: E = lam(i);
    match (v) { A(n, f) => { a = a + f(n); }, B => { a = a + 1; } }
    var w: E = viacall(i);
    match (w) { A(n, f) => { a = a + f(n); }, B => { a = a + 2; } }
    var unused: E = lam(i + 1);
    var g: (i32) => i32 = (x: i32): i32 => x - i;
    var p: E = wrap(i, g);
    a = a + use_e(p) + use_e(viacall(i + 1)) + g(50);
    return a;
}
` + closureVariantMain

// An enum in a struct field, matched through the field, and a struct local
// rebound in a loop whose first value holds a closure local that outlives it.
const closureVariantStructFieldSrc = `enum E { A(i32, (i32) => i32), B }
struct H { e: E, n: i32 }
function mk(k: i32): (i32) => i32 { return (x: i32): i32 => x + k; }
function round(i: i32): i32 {
    var a: i32 = 0;
    var h: H = H { e: A(i, (x: i32): i32 => x + i), n: i };
    match (h.e) { A(n, f) => { a = a + f(n); }, B => { a = a + 1; } }
    var keep: (i32) => i32 = (x: i32): i32 => x * 2;
    var hk: H = H { e: A(i, keep), n: 1 };
    var k: i32 = 0;
    while (k < 3) {
        hk = H { e: A(k, mk(k)), n: k };
        match (hk.e) { A(n, f) => { a = a + f(n); }, B => { a = a + 1; } }
        k = k + 1;
    }
    return a + h.n + hk.n + keep(3);
}
` + closureVariantMain

// The guard: the closure outlives the variant that held it. keep is stored
// into a variant released every iteration and called afterwards; an arm
// stores its binding to an outer local and another returns it, and both are
// called after the variant is gone.
const closureVariantOutlivesSrc = `enum E { A(i32, (i32) => i32), B }
function pick(i: i32): (i32) => i32 {
    var v: E = A(i, (x: i32): i32 => x + i);
    match (v) { A(n, f) => { return f; }, B => { return (x: i32): i32 => 0; } }
    return (x: i32): i32 => 1;
}
function round(i: i32): i32 {
    var keep: (i32) => i32 = (x: i32): i32 => x + i;
    var a: i32 = 0;
    var k: i32 = 0;
    while (k < 3) {
        var v: E = A(k, keep);
        match (v) { A(n, f) => { a = a + f(n); }, B => { a = a + 1; } }
        k = k + 1;
    }
    var out: (i32) => i32 = (x: i32): i32 => x;
    var w: E = A(i, (x: i32): i32 => x * 3);
    match (w) { A(n, f) => { out = f; }, B => { out = (x: i32): i32 => 0; } }
    var h: (i32) => i32 = pick(i);
    return a + keep(100) + out(2) + h(4);
}
` + closureVariantMain

const closureVariantMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 10) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`

// Wants are interpreter-confirmed.
var closureVariantPayloadCases = []struct {
	name string
	src  string
	want int
}{
	{"built", closureVariantBuiltSrc, 45},
	{"returned", closureVariantReturnedSrc, 24},
	{"struct_field", closureVariantStructFieldSrc, 81},
	{"outlives", closureVariantOutlivesSrc, 94},
}

func TestSelfHostClosureVariantPayloadX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range closureVariantPayloadCases {
		for _, lw := range vblockClosureBoth {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := cli.exitOf(t, tc.src, "x86-64-linux", "FERN_LEAKCHECK=1", lw.env)
				if exit != tc.want {
					t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				assertBalancedCensus(t, stderr)
				stderr, exit = cli.exitOf(t, tc.src, "x86-64-linux", "FERN_SANITIZE=1", lw.env)
				if exit != tc.want || forArrStructSanitizerFault(stderr, true) {
					t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer finding\n%s", exit, tc.want, stderr)
				}
			})
		}
	}
}

func TestSelfHostClosureVariantPayloadArm64(t *testing.T) {
	checkClosureVariantPayload(t, "arm64-linux")
}

func TestSelfHostClosureVariantPayloadWasm(t *testing.T) {
	checkClosureVariantPayload(t, "wasm32-wasi")
}

func checkClosureVariantPayload(t *testing.T, target string) {
	cli := buildSelfHostCLI(t)
	for _, tc := range closureVariantPayloadCases {
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
