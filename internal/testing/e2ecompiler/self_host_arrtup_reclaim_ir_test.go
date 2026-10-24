package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var arrtupReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"arrtup-churn", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let xs: (i32, i32[])[] = [(i, [i, i + 1]), (i + 1, [i + 2, i + 3])]; acc = (acc + xs[0].0) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let ys: (i32, i32[])[] = [(j, [j, j + 1]), (j + 1, [j + 2, j + 3])]; acc = (acc + ys[0].0) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"arrtup-borrow-full", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 5000) {
        let xs: (i32, i32[])[] = [(i, [i, i + 1]), (i + 1, [i + 2, i + 3])];
        acc = (acc + xs[0].0 + xs[1].1[0] + xs[0].1.len()) % 251;
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let ys: (i32, i32[])[] = [(j, [j, j + 1])]; acc = (acc + ys[0].1[1]) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"arrtup-escape-store-safe", `function main(): i32 {
    let keep: i32[] = [0, 0];
    let i: i32 = 0;
    while (i < 50) {
        let xs: (i32, i32[])[] = [(i, [i, i + 1])];
        keep = xs[0].1;
        i = i + 1;
    }
    let acc: i32 = keep[0] + keep[1];
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"arrtup-escape-call-safe", `function take(xs: i32[]): i32 { return xs[0]; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let xs: (i32, i32[])[] = [(i, [i, i + 1])];
        acc = (acc + take(xs[0].1)) % 251;
        i = i + 1;
    }
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"arrtup-escape-fn-safe", `function mk(n: i32): (i32, i32[])[] {
    let xs: (i32, i32[])[] = [(n, [n, n + 1]), (n + 1, [n + 2, n + 3])];
    return xs;
}
function main(): i32 {
    let a = mk(5);
    let v: i32 = a[0].0 + a[0].1[0] + a[1].1[1];
    if (__rc_underflow_count() != 0) { return 99; }
    return v;
}`, 18},
}

func TestSelfHostArrTupReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range arrtupReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = array-of-tuples leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
