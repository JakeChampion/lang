package e2ecompiler

import "testing"

// `__alloc_i32`, `__alloc_i64` and `__alloc_bool` are `__alloc_u8`'s typed
// siblings: n zeroed slots of the element type, owned by the caller. Each
// program reads the lengths and every slot, writes some back, and keeps a
// zero-length allocation beside the rest, so a wrong slot width or a stale
// block off the free list changes its exit status. The Go interpreter is
// the oracle on every target, and an exit stays under 126 for wasmtime.
var allocFilledCases = []struct {
	name string
	src  string
}{
	{"i32", `function main(): i32 {
    let a: i32[] = __alloc_i32(5);
    let e: i32[] = __alloc_i32(0);
    let t: i32 = a.len() * 10 + e.len();
    let i: i32 = 0;
    while (i < a.len()) { t = t + a[i]; i = i + 1; }
    a = a.with(2, 7);
    return t + a[2] + a[3];
}`},
	{"i64", `function main(): i32 {
    let b: i64[] = __alloc_i64(3);
    let t: i64 = b.len() as i64 * 10i64;
    let i: i32 = 0;
    while (i < b.len()) { t = t + b[i]; i = i + 1; }
    b = b.with(1, 9i64);
    return (t + b[1] + b[0]) as i32;
}`},
	{"bool", `function main(): i32 {
    let c: boolean[] = __alloc_bool(4);
    let t: i32 = c.len() * 10;
    let i: i32 = 0;
    while (i < c.len()) { if (c[i]) { t = t + 100; } i = i + 1; }
    c = c.with(1, true);
    if (c[1] && !c[0] && !c[3]) { t = t + 1; }
    return t;
}`},
	{"beside_each_other", `function main(): i32 {
    let a: i32[] = __alloc_i32(5);
    let b: i64[] = __alloc_i64(3);
    let c: boolean[] = __alloc_bool(4);
    let d: u8[] = __alloc_u8(6);
    let t: i32 = a.len() + b.len() * 10 + c.len() * 20 + d.len();
    let i: i32 = 0;
    while (i < a.len()) { t = t + a[i]; i = i + 1; }
    i = 0;
    while (i < b.len()) { t = t + b[i] as i32; i = i + 1; }
    i = 0;
    while (i < c.len()) { if (c[i]) { t = t + 7; } i = i + 1; }
    i = 0;
    while (i < d.len()) { t = t + d[i] as i32; i = i + 1; }
    return t;
}`},
}

func TestSelfHostAllocFilled(t *testing.T) {
	cli := buildSelfHostCLI(t)
	fern := buildLangBinForInterp(t)
	for _, tc := range allocFilledCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExitStdin(t, fern, tc.src+"\n", "")
			if want == 0 || want == 1 || want > 125 {
				t.Fatalf("the interpreter exited %d: 0 and 1 tell a wrong answer from nothing, and wasmtime rejects an exit above 125", want)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				if _, code := cli.exitOf(t, tc.src+"\n", target); code != want {
					t.Errorf("%s: exit %d, want %d (interp oracle)", target, code, want)
				}
			}
		})
	}
}
