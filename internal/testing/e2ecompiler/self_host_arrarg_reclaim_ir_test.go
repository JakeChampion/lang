package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var arrArgReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"arrarg-churn-flat", `function take(xs: i32[]): i32 {
    return xs[0] + xs[1];
}
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { acc = (acc + take([w, w + 1])) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { acc = (acc + take([i, i + 1])) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"arrarg-multi-fwd-flat", `function take2(xs: i32[], ys: i32[]): i32 {
    return xs[0] + ys[1];
}
function fwd(xs: i32[]): i32 {
    return take2(xs, xs);
}
function fsum(xs: f64[]): i32 {
    if (xs[0] + xs[1] > 2.9) { return 3; }
    return 2;
}
function slen(xs: string[]): i32 {
    return xs[0].len() + xs.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) {
        acc = (acc + take2([w, w], [w, w + 1])) % 251;
        acc = (acc + fwd([w, w + 2])) % 251;
        acc = (acc + fsum([1.5, 1.5])) % 251;
        acc = (acc + slen(["ab", "c"])) % 251;
        w = w + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) {
        acc = (acc + take2([i, i], [i, i + 1])) % 251;
        acc = (acc + fwd([i, i + 2])) % 251;
        i = i + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    let f3: i32 = fsum([2.0, 1.0]);
    if (f3 != 3) { return 96; }
    let s3: i32 = slen(["xy", "z"]);
    if (s3 != 4) { return 95; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 94; }
    return 0;
}`, 0},
	{"arrarg-escape-safe", `function keep(xs: i32[]): i32[] {
    return xs;
}
function main(): i32 {
    let k: i32[] = keep([7, 8]);
    let acc: i32 = k[0] + k[1];
    if (__rc_underflow_count() != 0) { return 99; }
    return acc;
}`, 15},
	{"arrarg-consuming-callee-safe", `function mut(xs: i32[]): i32 {
    xs = xs.append(9);
    return xs[2] + xs.len();
}
function mut2(xs: i32[]): i32 {
    let ys: i32[] = xs.append(7);
    return ys[2] + ys.len();
}
function main(): i32 {
    let r: i32 = mut([4, 5]);
    if (r != 12) { return 97; }
    let v: i32[] = [1, 2];
    let r2: i32 = mut2(v);
    if (r2 != 10) { return 96; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"arrarg-strarr-producer-counted-flat", `struct Node { name: string, deps: string[], mtime: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function node(name: string, deps: string[], mtime: i32): Node { return Node { name: name, deps: deps, mtime: mtime }; }
function round(pre: string, n: i32): i32 { let f: Node = node(w(pre), deps_of(pre), n); return f.deps.len() + f.name.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre, i)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let w0: i32 = churn(2000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(2000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (w0 != x) { return 97; }
    return 0;
}`, 0},
	{"arrarg-strarr-counted-held-struct", `struct Held { deps: string[], n: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string, n: i32): string[] { let out: string[] = []; let i: i32 = 0; while (i < (n % 5) + 1) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(p: string[], n: i32): Held { return Held { deps: p, n: n }; }
function round(pre: string, n: i32): i32 {
    let h: Held = keep(deps_of(pre, n), n);
    let k1: string[] = deps_of(pre, n + 1);
    let k2: string[] = deps_of(pre, n + 3);
    let k3: string[] = deps_of(pre, n + 7);
    if (k1.len() < 0 || k2.len() < 0 || k3.len() < 0) { return 0; }
    return h.deps.len() + h.n - n;
}
function main(): i32 { let pre: string = "ab"; let i: i32 = 0; while (i < 2000) { if (round(pre, i) != (i % 5) + 1) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	{"arrarg-strarr-read-only-callee-safe", `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function count(p: string[]): i32 { return p.len() + p.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let i: i32 = 0; while (i < n) { if (count(deps_of(pre)) != 6) { return 97; } i = i + 1; } return 0; }
function main(): i32 { let a: i32 = churn(2000); if (a != 0) { return a; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	{"arrarg-scalar-struct-ret-alias-safe", `struct SIn { a: i32[] }
struct SOut { b: i32[] }
function nums_of(n: i32): i32[] { let out: i32[] = []; let i: i32 = 0; while (i < 4) { out = out.append(n + i); i = i + 1; } return out; }
function esci(p: i32[]): SOut { let q: SIn = SIn { a: p }; return SOut { b: q.a }; }
function round(n: i32): i32 { let o: SOut = esci(nums_of(n)); let k: i32[] = nums_of(n + 9); if (k.len() < 0) { return 0; } return o.b.len() + o.b[0] - n + o.b[3] - n; }
function main(): i32 { let i: i32 = 0; while (i < 2000) { if (round(i) != 7) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
}

func TestSelfHostArrArgReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range arrArgReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
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
				t.Errorf("%s = %d, want %d (98 = arg temp leaked; 99 = over-release/underflow; 94-97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
