package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var heapBumpBytesIRCases = []struct {
	name string
	main string
	want int
}{
	{"zero-before-alloc", `function main(): i32 { if ((__heap_bump_bytes() as i32) == 0) { return 7; } return 1; }`, 7},
	{"grows-on-alloc", `function main(): i32 { let before: i32 = (__heap_bump_bytes() as i32); let a: i32[] = [before, 2, 3, 4, 5]; let after: i32 = (__heap_bump_bytes() as i32); if (before == 0) { if (after > before) { return 7; } } return 1; }`, 7},
	{"after-positive", `function main(): i32 { let a: i32[] = [__heap_bump_bytes() as i32, 2, 3]; if ((__heap_bump_bytes() as i32) > 0) { return 11; } return 1; }`, 11},
	{"i64-typed", `function main(): i32 { let before: i64 = __heap_bump_bytes(); let a: i32[] = [before as i32, 2, 3]; let after: i64 = __heap_bump_bytes(); if (before != (0 as i64)) { return 1; } if (after <= before) { return 2; } return 9; }`, 9},
}

func TestSelfHostHeapBumpBytesIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range heapBumpBytesIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}

var heapBumpFixpointCases = []struct {
	name string
	src  func(n string) string
}{
	{"precise-drop-array", func(n string) string {
		return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let v: i32[] = [i, i + 1, i + 2]; acc = acc + v[0] + v[1] + v[2]; i = i + 1; }
    if (acc == 987654) { return 200; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	{"loop-reassign-array", func(n string) string {
		return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let v: i32[] = [i, i + 1]; v = [i, i + 2, i + 3]; acc = acc + v[0] + v[1] + v[2]; i = i + 1; }
    if (acc == 987654) { return 200; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	{"literal-alloc", func(n string) string {
		return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let b: u8[] = __alloc_u8(8); b = b.with(0, (i % 200) as u8); acc = acc + (b[0] as i32); i = i + 1; }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
}

func TestSelfHostHeapBumpFixpointX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../../compiler", "drivers/asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	shGrowth := func(t *testing.T, tag, prog string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", tag)
		}
		progBin := buildBin(t, gcc, dir, tag, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	const small, large = "50", "5000"
	for _, tc := range heapBumpFixpointCases {
		t.Run(tc.name, func(t *testing.T) {
			shS := shGrowth(t, tc.name+"-50", tc.src(small))
			shL := shGrowth(t, tc.name+"-5000", tc.src(large))
			if shS != shL {
				t.Errorf("%s: self-host high-water not bounded (N=50 -> %d, N=5000 -> %d)", tc.name, shS, shL)
			}
			if shS == 0 {
				t.Errorf("%s: self-host growth is 0 — nothing allocated / measured", tc.name)
			}
		})
	}
}
