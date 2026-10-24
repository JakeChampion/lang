package e2ecompiler

import (
	"testing"
)

var closurePtrCaptureIRCases = []struct {
	name string
	src  string
}{
	{"string_capture", `enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32, acc: string): Fut {
    function resume(woken: i32): Fut { return Rdy(woken + acc.len()); }
    return Pend(c, resume);
}
function main(): i32 {
    let f: Fut = drain(0, "hello");
    match (f) { Rdy(v) => { return v; }, Pend(fd, k) => { let r: Fut = k(37); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } } }
    return 99;
}`},
	{"struct_capture", `struct P { x: i32, y: i32 }
enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32, p: P): Fut {
    function resume(w: i32): Fut { return Rdy(w + p.x + p.y); }
    return Pend(c, resume);
}
function main(): i32 {
    let f: Fut = drain(0, P { x: 30, y: 11 });
    match (f) { Rdy(v) => { return v; }, Pend(fd, k) => { let r: Fut = k(1); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } } }
    return 99;
}`},
	{"array_capture", `enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32, xs: i32[]): Fut {
    function resume(w: i32): Fut { return Rdy(w + xs.len() + xs[0]); }
    return Pend(c, resume);
}
function main(): i32 {
    let f: Fut = drain(0, [40, 7, 8]);
    match (f) { Rdy(v) => { return v; }, Pend(fd, k) => { let r: Fut = k(0); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } } }
    return 99;
}`},
	{"mixed_i32_string", `enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32, n: i32, s: string): Fut {
    function resume(w: i32): Fut { return Rdy(w + n + s.len()); }
    return Pend(c, resume);
}
function main(): i32 {
    let f: Fut = drain(0, 20, "abcde");
    match (f) { Rdy(v) => { return v; }, Pend(fd, k) => { let r: Fut = k(17); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } } }
    return 99;
}`},
}

func TestSelfHostClosurePtrCaptureIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range closurePtrCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(src))

			if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != want {
				t.Errorf("%q exited %d, want %d (interp oracle)\n%s", tc.name, code, want, stderr)
			}
		})
	}
}
