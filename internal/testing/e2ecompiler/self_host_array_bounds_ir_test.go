package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostArrayBoundsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"read-past-end",
			`function main(): i32 { let a: i32[] = [1, 2, 3]; return a[5]; }`, 134},
		{"read-negative",
			`function main(): i32 { let a: i32[] = [1, 2, 3]; let i: i32 = 0 - 1; return a[i]; }`, 134},
		{"read-at-len",
			`function main(): i32 { let a: i32[] = [1, 2, 3]; return a[3]; }`, 134},
		{"write-past-end",
			`function main(): i32 { let a: i32[] = [1, 2, 3]; a = a.with(5, 9); return a[0]; }`, 134},
		{"in-range-ok",
			`function main(): i32 { let a: i32[] = [10, 20, 30]; a = a.with(1, 20); let s: i32 = 0; let i: i32 = 0; while (i < 3) { s = s + a[i]; i = i + 1; } return s; }`, 60},
		{"str-read-past-end",
			`function idx(): i32 { return 5; }
function main(): i32 { let s: string = "abc"; return s[idx()] as i32; }`, 134},
		{"str-read-at-len",
			`function idx(): i32 { return 3; }
function main(): i32 { let s: string = "abc"; return s[idx()] as i32; }`, 134},
		{"str-read-negative",
			`function idx(): i32 { return 0 - 1; }
function main(): i32 { let s: string = "abc"; return s[idx()] as i32; }`, 134},
		{"str-in-range-ok",
			`function idx(i: i32): i32 { return i; }
function main(): i32 { let s: string = "abc"; return (s[idx(0)] as i32) + (s[idx(2)] as i32); }`, 196},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("%s: driver failed: %v", tc.name, err)
			}
			if !strings.Contains(string(asm), ".Lssa_") {
				t.Fatalf("%s: did not emit through the register path (no .Lssa_ labels)", tc.name)
			}
			if tc.want == 134 && !strings.Contains(string(asm), "__fern_oob_abort") {
				t.Fatalf("%s: no __fern_oob_abort in emitted asm — bounds check missing", tc.name)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var run *exec.Cmd
			if len(runner) == 0 {
				run = exec.Command(bin)
			} else {
				run = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostArrayBoundsIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"read-past-end",
			`function main(): i32 { let a: i32[] = [1, 2, 3]; return a[5]; }`, 134},
		{"read-negative",
			`function main(): i32 { let a: i32[] = [1, 2, 3]; let i: i32 = 0 - 1; return a[i]; }`, 134},
		{"write-past-end",
			`function main(): i32 { let a: i32[] = [1, 2, 3]; a = a.with(5, 9); return a[0]; }`, 134},
		{"in-range-ok",
			`function main(): i32 { let a: i32[] = [10, 20, 30]; a = a.with(1, 20); let s: i32 = 0; let i: i32 = 0; while (i < 3) { s = s + a[i]; i = i + 1; } return s; }`, 60},
		{"str-read-past-end",
			`function idx(): i32 { return 5; }
function main(): i32 { let s: string = "abc"; return s[idx()] as i32; }`, 134},
		{"str-read-negative",
			`function idx(): i32 { return 0 - 1; }
function main(): i32 { let s: string = "abc"; return s[idx()] as i32; }`, 134},
		{"str-in-range-ok",
			`function idx(i: i32): i32 { return i; }
function main(): i32 { let s: string = "abc"; return (s[idx(0)] as i32) + (s[idx(2)] as i32); }`, 196},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			if tc.want == 134 && !strings.Contains(string(asm), "__fern_oob_abort") {
				t.Fatalf("%s: no __fern_oob_abort in emitted arm64 asm — bounds check missing", tc.name)
			}
			progBin := buildBin(t, arm64gcc, dir, "arr_bounds_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
