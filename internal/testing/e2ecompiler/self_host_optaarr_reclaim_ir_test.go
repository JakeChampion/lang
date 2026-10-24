package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var optAarrReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"optaarr-churn-flat", `function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { let xs: Option[i32[]][] = [Some([w, w + 1]), None]; acc = (acc + xs.len()) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let xs2: Option[i32[]][] = [Some([i, i + 1]), None]; acc = (acc + xs2.len()) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"optaarr-payload-borrow-flat", `function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) {
        let xs: Option[i32[]][] = [Some([w, w + 1]), None];
        match (xs[0]) { Some(p) => { acc = acc + p[0] + p[1]; }, None => {} }
        match (xs[1]) { Some(q) => { acc = acc + q[0]; }, None => { acc = acc + 1; } }
        w = w + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) {
        let xs2: Option[i32[]][] = [Some([i, i + 1]), None];
        match (xs2[0]) { Some(p) => { acc = (acc + p[0] + p[1]) % 251; }, None => {} }
        i = i + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"optaarr-elem-alias-safe", `function main(): i32 {
    let xs: Option[i32[]][] = [Some([7, 8]), None];
    let o = xs[0];
    let acc: i32 = 0;
    match (o) { Some(p) => { acc = p[0] + p[1]; }, None => {} }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc;
}`, 15},
	{"optaarr-payload-escape-safe", `function pick(xs: Option[i32[]][]): i32[] {
    match (xs[0]) { Some(p) => { return p; }, None => {} }
    return [0];
}
function main(): i32 {
    let xs: Option[i32[]][] = [Some([5, 6]), None];
    let p = pick(xs);
    if (__rc_underflow_count() != 0) { return 99; }
    return p[0] + p[1];
}`, 11},
}

func TestSelfHostOptAarrReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range optAarrReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = structure leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
