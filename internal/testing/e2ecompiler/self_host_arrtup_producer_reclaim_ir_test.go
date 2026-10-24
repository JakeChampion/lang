package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var arrtupProducerReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"arrtup-producer-churn", `function mk(n: i32): (i32, i32[])[] { return [(n, [n, n + 1]), (n + 1, [n + 2, n + 3])]; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let ps: (i32, i32[])[] = mk(i); acc = (acc + ps[0].0) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let qs: (i32, i32[])[] = mk(j); acc = (acc + qs[0].0) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"arrstruct-producer-churn", `struct Q { xs: i32[] }
function mkqs(n: i32): Q[] { return [Q { xs: [n, n + 1] }, Q { xs: [n + 2, n + 3] }]; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let ps: Q[] = mkqs(i); acc = (acc + ps[0].xs[0]) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let qs: Q[] = mkqs(j); acc = (acc + qs[0].xs[0]) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"arrtup-producer-value", `struct Q { xs: i32[] }
function mkps(n: i32): (i32, i32[])[] { return [(n, [n, n + 1]), (n + 1, [n + 2, n + 3])]; }
function mkqs(n: i32): Q[] { return [Q { xs: [n, n + 1] }]; }
function main(): i32 {
    let ps: (i32, i32[])[] = mkps(5);
    let qs: Q[] = mkqs(3);
    let v: i32 = ps[0].0 + ps[0].1[0] + ps[1].1[1] + ps.len() + qs[0].xs[0] + qs[0].xs[1] + qs.len();
    if (__rc_underflow_count() != 0) { return 99; }
    return v;
}`, 28},
	{"arrtup-producer-nonfresh-safe", `function mkbad(xs: i32[], n: i32): (i32, i32[])[] { return [(n, xs)]; }
function main(): i32 {
    let shared: i32[] = [1, 2];
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let ps: (i32, i32[])[] = mkbad(shared, i);
        acc = (acc + ps[0].0 + ps[0].1[0]) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (shared[0] + shared[1] != 3) { return 97; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"arrtup-producer-forwarded-safe", `function mk(n: i32): (i32, i32[])[] { return [(n, [n, n + 1])]; }
function keepit(n: i32): (i32, i32[])[] { let ps: (i32, i32[])[] = mk(n); return ps; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { let r: (i32, i32[])[] = keepit(i); acc = (acc + r[0].0 + r[0].1[1]) % 251; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"arrtup-producer-elem-alias-safe", `function mk(n: i32): (i32, i32[])[] { return [(n, [n, n + 1])]; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let ps: (i32, i32[])[] = mk(i);
        let t: (i32, i32[]) = ps[0];
        acc = (acc + t.0 + t.1[1] + ps[0].1[0]) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
}

func TestSelfHostArrTupProducerReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range arrtupProducerReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = call-bound array-of-boxes leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
