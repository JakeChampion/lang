package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var arrOwnedRetReleaseCases = []struct {
	name string
	src  string
	want int
}{
	{"arrown-method-len-recv", `struct H { xs: i32[] }
function (h: H) get(): i32[] { return h.xs; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let keep: H = H { xs: [1, 2, 3] }; t = t + keep.get().len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"arrown-method-discarded", `struct H { xs: i32[] }
function (h: H) get(): i32[] { return h.xs; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let keep: H = H { xs: [1, 2, 3] }; keep.get(); t = t + keep.xs[0]; i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"arrown-free-fn-index", `struct H { xs: i32[] }
function grab(h: H): i32[] { return h.xs; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let keep: H = H { xs: [1, 2, 3] }; t = t + grab(keep)[1]; i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"arrown-borrowed-param-len-recv", `@noinline function id(a: i32[]): i32[] { return a; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let s: i32[] = [1, 2, 3]; t = t + id(s).len() + s[0]; i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"arrown-mixed-returns", `struct H { xs: i32[] }
function pick(h: H, c: i32): i32[] { if (c > 0) { return h.xs; } return [7, 8]; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let keep: H = H { xs: [1, 2, 3] }; t = t + pick(keep, i % 2).len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"arrown-bound-result-balanced", `struct H { xs: i32[] }
function (h: H) get(): i32[] { return h.xs; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let keep: H = H { xs: [1, 2, 3] };
        let a: i32[] = keep.get();
        t = t + a.len() + a[2] + keep.xs[0];
        i = i + 1;
    }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"arrown-sibling-field-deep-drop", `struct H2 { xs: i32[], ys: i32[] }
function (h: H2) get(): i32[] { return h.xs; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let keep: H2 = H2 { xs: [1, 2, 3], ys: [4, 5, 6, 7, 8] };
        t = t + keep.get().len() + keep.ys.len();
        i = i + 1;
    }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"arrown-shared-buffer-still-live", `struct H { xs: i32[] }
function (h: H) get(): i32[] { return h.xs; }
function churn(n: i32): i32 {
    let keep: H = H { xs: [11, 22, 33] };
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = (t + keep.get().len()) % 251; i = i + 1; }
    let d1: i32[] = [777, 888, 999];
    let d2: i32[] = [111, 222, 333];
    return (t + keep.xs[0] + keep.xs[1] + keep.xs[2] + d1[0] + d2[0]) % 9973;
}
function main(): i32 {
    let w: i32 = churn(400);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != 1150) { return 97; }
    return 0;
}`, 0},
	{"arrown-own-param-refused", `@noinline function id(own a: i32[]): i32[] { return a; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + id([1, 2, 3]).len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let x: i32 = churn(200);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return 0;
}`, 0},

	{"strarr-producer-len-recv", `function w(a: string): string { return a + "!"; }
function mks(): string[] { return [w("a"), w("b"), w("c")]; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + mks().len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"strarr-producer-discarded", `function w(a: string): string { return a + "!"; }
function mks(): string[] { return [w("a"), w("b"), w("c")]; }
function churn(n: i32): i32 {
    let i: i32 = 0;
    while (i < n) { mks(); i = i + 1; }
    return n % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"strarr-producer-index-unchanged", `function w(a: string): string { return a + "!"; }
function mks(): string[] { return [w("a"), w("b"), w("c")]; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + mks()[1].len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"strarr-producer-bound-balanced", `function w(a: string): string { return a + "!"; }
function mks(): string[] { return [w("a"), w("b"), w("c")]; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let v: string[] = mks(); t = t + v.len() + v[0].len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"strarr-field-len-not-deep-freed", `function w(a: string): string { return a + "!"; }
struct H { xs: string[] }
function (h: H) get(): string[] { return h.xs; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let keep: H = H { xs: [w("a"), w("b"), w("c")] };
        t = t + keep.get().len() + keep.get().len() + keep.xs[0].len() + keep.xs[2].len();
        i = i + 1;
    }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    if (w1 != (200 * 10) % 251) { return 96; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"arrstruct-producer-len-recv", `struct Inner { k: i32, ys: i32[] }
function mk(): Inner[] { return [Inner { k: 1, ys: [1, 2] }, Inner { k: 2, ys: [3] }]; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + mk().len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"arrstruct-producer-discarded", `struct Inner { k: i32, ys: i32[] }
function mk(): Inner[] { return [Inner { k: 1, ys: [1, 2] }, Inner { k: 2, ys: [3] }]; }
function churn(n: i32): i32 {
    let i: i32 = 0;
    while (i < n) { mk(); i = i + 1; }
    return n % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"structarr-producer-len-recv", `function w(a: string): string { return a + "!"; }
struct P { s: string, n: i32 }
function mkp(): P[] { return [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }]; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + mkp().len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"structarr-producer-discarded", `function w(a: string): string { return a + "!"; }
struct P { s: string, n: i32 }
function mkp(): P[] { return [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }]; }
function churn(n: i32): i32 {
    let i: i32 = 0;
    while (i < n) { mkp(); i = i + 1; }
    return n % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"struct-producer-reassigned-local", `struct P { xs: i32[], s: string }
function w(s: string): string { return s + ""; }
function re(i: i32): P { let p: P = P { xs: [i], s: w("r") }; p = P { xs: [i, i, i], s: w("q") }; return p; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let c: P = re(i); t = t + c.xs.len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"struct-producer-reassigned-alias-refused", `struct P { xs: i32[], s: string }
function w(s: string): string { return s + ""; }
function re2(i: i32): P {
    let p: P = P { xs: [i], s: w("r") };
    let q: P = p;
    p = P { xs: [i, i, i], s: w("q") };
    if (q.xs.len() != 1) { return p; }
    return p;
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let c: P = re2(i); t = t + c.xs.len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let x: i32 = churn(200);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    if (w1 != (200 * 3) % 251) { return 96; }
    return 0;
}`, 0},

	{"structarr-scalar-producer-len-recv", `struct Q { a: i32, b: i32 }
function mkq(): Q[] { return [Q { a: 1, b: 2 }, Q { a: 3, b: 4 }]; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + mkq().len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"arrstruct-producer-index-field", `struct Inner { k: i32, ys: i32[] }
function mk(): Inner[] { return [Inner { k: 1, ys: [1, 2] }, Inner { k: 2, ys: [3] }]; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + mk()[0].k; i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"structarr-producer-index-field", `function w(a: string): string { return a + "!"; }
struct P { s: string, n: i32 }
function mkp(): P[] { return [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }]; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + mkp()[1].n; i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"structarr-producer-index-strfield-not-freed", `function w(a: string): string { return a + "!"; }
struct P { s: string, n: i32 }
function mkp(): P[] { return [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }]; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + mkp()[0].s.len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let x: i32 = churn(200);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    if (w1 != (200 * 2) % 251) { return 96; }
    return 0;
}`, 0},

	{"arrstruct-producer-bound-balanced", `struct Inner { k: i32, ys: i32[] }
function mk(): Inner[] { return [Inner { k: 1, ys: [1, 2] }, Inner { k: 2, ys: [3] }]; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let v: Inner[] = mk(); t = t + v.len() + v[0].k; i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},

	{"arrstruct-field-len-not-deep-freed", `struct Inner { k: i32, ys: i32[] }
struct H { xs: Inner[] }
function (h: H) get(): Inner[] { return h.xs; }
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let keep: H = H { xs: [Inner { k: 3, ys: [1] }, Inner { k: 4, ys: [2] }] };
        t = t + keep.get().len() + keep.xs[0].k + keep.xs[1].k;
        i = i + 1;
    }
    return t % 251;
}
function main(): i32 {
    let w1: i32 = churn(200);
    let x: i32 = churn(200);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w1 != x) { return 97; }
    if (w1 != (200 * 9) % 251) { return 96; }
    return 0;
}`, 0},
}

const arrOwnedRetFailFmt = "%s = %d, want %d (a small non-zero is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostArrOwnedRetReleaseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrOwnedRetReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(arrOwnedRetFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostArrOwnedRetReleaseIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrOwnedRetReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(arrOwnedRetFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostArrOwnedRetReleaseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping owned array return release wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range arrOwnedRetReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf(arrOwnedRetFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
