package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var optArrArrReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"optarrarr-churn", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let o: Option[i32[][]] = Some([[i, i + 1], [i + 2, i + 3]]); match (o) { Some(g) => { acc = (acc + g[0][0]) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[i32[][]] = Some([[j, j + 1]]); match (o2) { Some(g) => { acc = (acc + g[0][0]) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"optarrarr-borrow-full", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 5000) { let o: Option[i32[][]] = Some([[i, i + 1], [i + 2, i + 3]]); match (o) { Some(g) => { acc = (acc + g[0][0] + g[1][1] + g.len() + g[0].len()) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[i32[][]] = Some([[j, j + 1]]); match (o2) { Some(g) => { acc = (acc + g[0][1]) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"optarrarr-escape-store-safe", `function main(): i32 {
    let keep: i32[] = [0, 0];
    let i: i32 = 0;
    while (i < 50) {
        let o: Option[i32[][]] = Some([[i, i + 1]]);
        match (o) { Some(g) => { keep = g[0]; }, None => {} }
        i = i + 1;
    }
    let acc: i32 = keep[0] + keep[1];
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"optarrarr-escape-call-safe", `function take(a: i32[]): i32 { return a[0]; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let o: Option[i32[][]] = Some([[i, i + 1]]);
        match (o) { Some(g) => { acc = (acc + take(g[0])) % 251; }, None => {} }
        i = i + 1;
    }
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

func TestSelfHostOptArrArrReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range optArrArrReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = option leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
