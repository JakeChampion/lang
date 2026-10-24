package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var arrStructReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"arrstruct-churn", `struct P { xs: i32[] }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let ps: P[] = [P { xs: [i, i + 1] }, P { xs: [i + 2, i + 3] }]; acc = (acc + ps[0].xs[0]) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let qs: P[] = [P { xs: [j, j + 1] }]; acc = (acc + qs[0].xs[0]) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"arrstruct-borrow-full", `struct P { n: i32, xs: i32[] }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 5000) {
        let ps: P[] = [P { n: i, xs: [i, i + 1] }, P { n: i + 1, xs: [i + 2, i + 3] }];
        acc = (acc + ps[0].n + ps[1].xs[0] + ps[0].xs.len()) % 251;
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let qs: P[] = [P { n: j, xs: [j, j + 1] }]; acc = (acc + qs[0].xs[1]) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"arrstruct-escape-store-safe", `struct P { xs: i32[] }
function main(): i32 {
    let keep: i32[] = [0, 0];
    let i: i32 = 0;
    while (i < 50) {
        let ps: P[] = [P { xs: [i, i + 1] }];
        keep = ps[0].xs;
        i = i + 1;
    }
    let acc: i32 = keep[0] + keep[1];
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"arrstruct-escape-call-safe", `struct P { xs: i32[] }
function take(xs: i32[]): i32 { return xs[0]; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let ps: P[] = [P { xs: [i, i + 1] }];
        acc = (acc + take(ps[0].xs)) % 251;
        i = i + 1;
    }
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"arrstruct-escape-fn-safe", `struct P { xs: i32[] }
function mk(n: i32): P[] {
    let ps: P[] = [P { xs: [n, n + 1] }, P { xs: [n + 2, n + 3] }];
    return ps;
}
function main(): i32 {
    let a = mk(5);
    let v: i32 = a[0].xs[0] + a[1].xs[1];
    if (__rc_underflow_count() != 0) { return 99; }
    return v;
}`, 13},
}

func TestSelfHostArrStructReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range arrStructReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = array-of-structs leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
