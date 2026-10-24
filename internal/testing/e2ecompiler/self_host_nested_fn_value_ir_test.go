package e2ecompiler

import (
	"testing"
)

var nestedFnValueIRCases = []struct {
	name string
	src  string
}{
	{"nocap_payload", `enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32): Fut {
    function resume(woken: i32): Fut { return Rdy(woken + 41); }
    return Pend(c, resume);
}
function main(): i32 {
    let f: Fut = drain(0);
    match (f) {
        Rdy(v) => { return v; },
        Pend(fd, k) => { let r: Fut = k(1); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } }
    }
    return 99;
}`},
	{"i32cap_payload", `enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32, acc: i32): Fut {
    function resume(woken: i32): Fut { return Rdy(acc + woken); }
    return Pend(c, resume);
}
function main(): i32 {
    let f: Fut = drain(0, 41);
    match (f) {
        Rdy(v) => { return v; },
        Pend(fd, k) => { let r: Fut = k(1); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } }
    }
    return 99;
}`},
	{"multicap_payload", `enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32, a: i32, b: i32): Fut {
    function resume(woken: i32): Fut { return Rdy(woken + a + b); }
    return Pend(c, resume);
}
function main(): i32 {
    let f: Fut = drain(0, 30, 11);
    match (f) {
        Rdy(v) => { return v; },
        Pend(fd, k) => { let r: Fut = k(1); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } }
    }
    return 99;
}`},
	{"hof_arg", `function apply(g: (i32) => i32, n: i32): i32 { return g(n); }
function mk(base: i32): i32 {
    let add = (x: i32): i32 => { return x + base; };
    return apply(add, 9);
}
function main(): i32 { return mk(33); }`},
}

func TestSelfHostNestedFnValueIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range nestedFnValueIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(src))

			if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != want {
				t.Errorf("%q exited %d, want %d (interp oracle)\n%s", tc.name, code, want, stderr)
			}
		})
	}
}
