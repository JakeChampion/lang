package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var tupStructFieldReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"arrtupstruct-churn", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let xs: (i32, P)[] = [(i, P { xs: [i, i + 1], y: i })]; acc = (acc + xs[0].0) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let ys: (i32, P)[] = [(j, P { xs: [j, j + 1], y: j })]; acc = (acc + ys[0].1.y) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"arrtupstruct-borrow-full", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 5000) { let xs: (i32, P)[] = [(i, P { xs: [i, i + 1], y: i })]; acc = (acc + xs[0].0 + xs[0].1.y + xs[0].1.xs[0] + xs[0].1.xs.len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let ys: (i32, P)[] = [(j, P { xs: [j, j + 1], y: j })]; acc = (acc + ys[0].1.xs[1]) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"arrtupstruct-escape-store-safe", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    let keep: P = P { xs: [0, 0], y: 0 };
    let i: i32 = 0;
    while (i < 50) { let xs: (i32, P)[] = [(i, P { xs: [i, i + 1], y: i })]; keep = xs[0].1; i = i + 1; }
    let acc: i32 = keep.xs[0] + keep.y;
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"arrtupstruct-escape-arrfield-safe", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    let keep: i32[] = [0, 0];
    let i: i32 = 0;
    while (i < 50) { let xs: (i32, P)[] = [(i, P { xs: [i, i + 1], y: i })]; keep = xs[0].1.xs; i = i + 1; }
    let acc: i32 = keep[0] + keep[1];
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"opttupstruct-churn", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let o: Option[(i32, P)] = Some((i, P { xs: [i, i + 1], y: i })); match (o) { Some(g) => { acc = (acc + g.0) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[(i32, P)] = Some((j, P { xs: [j, j + 1], y: j })); match (o2) { Some(g) => { acc = (acc + g.1.y) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"opttupstruct-borrow-full", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 5000) { let o: Option[(i32, P)] = Some((i, P { xs: [i, i + 1], y: i })); match (o) { Some(g) => { acc = (acc + g.0 + g.1.y + g.1.xs[0] + g.1.xs.len()) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[(i32, P)] = Some((j, P { xs: [j, j + 1], y: j })); match (o2) { Some(g) => { acc = (acc + g.1.xs[1]) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"opttupstruct-escape-store-safe", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    let keep: P = P { xs: [0, 0], y: 0 };
    let i: i32 = 0;
    while (i < 50) { let o: Option[(i32, P)] = Some((i, P { xs: [i, i + 1], y: i })); match (o) { Some(g) => { keep = g.1; }, None => {} } i = i + 1; }
    let acc: i32 = keep.xs[0] + keep.y;
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"arrtup-plain-array-regression", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 5000) { let xs: (i32, i32[])[] = [(i, [i, i + 1])]; acc = (acc + xs[0].0 + xs[0].1[0] + xs[0].1.len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let ys: (i32, i32[])[] = [(j, [j, j + 1])]; acc = (acc + ys[0].1[1]) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    return 0;
}`, 0},
}

func TestSelfHostTupStructFieldReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range tupStructFieldReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
