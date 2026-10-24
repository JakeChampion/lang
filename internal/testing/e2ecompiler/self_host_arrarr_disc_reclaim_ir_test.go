package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var arrArrDiscReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"arrarr-disc-churn", `function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { [[w, w + 1], [w]]; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { [[i, i + 1], [i]]; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"arrarr-disc-f64", `function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { [[1.5, 2.5], [3.5]]; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { [[1.5, 2.5], [3.5]]; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"arrarr-disc-alias-safe", `function main(): i32 {
    let inner: i32[] = [7, 8];
    [[9], inner];
    let ok: i32 = inner[0] + inner[1];
    if (ok != 15) { return 97; }
    let w: i32 = 0;
    while (w < 100) { [[w], [w, w + 1]]; w = w + 1; }
    let again: i32 = inner[0] + inner[1];
    if (again != 15) { return 96; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"arrarr-disc-strinner-safe", `function main(): i32 {
    let w: i32 = 0;
    while (w < 100) { [["a" + "x"], ["b", "c"]]; w = w + 1; }
    let chk: i32 = 42;
    if (chk != 42) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

func TestSelfHostArrArrDiscReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range arrArrDiscReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = arrarr temp leaked; 99 = over-release/underflow; 96-97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
