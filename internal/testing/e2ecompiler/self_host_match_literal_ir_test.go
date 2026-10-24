package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostMatchLiteralIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	emitAndRunIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s", err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"i32-first", `function main(): i32 { let n: i32 = 1; match (n) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 0; } } }`, 10},
		{"i32-middle", `function main(): i32 { let n: i32 = 2; match (n) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 0; } } }`, 20},
		{"i32-default", `function main(): i32 { let n: i32 = 9; match (n) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 7; } } }`, 7},
		{"i32-literal-scrutinee", `function main(): i32 { match (3) { 1 => { return 1; }, 3 => { return 33; }, _ => { return 0; } } }`, 33},
		{"string-match", `function main(): i32 { let s: string = "b"; match (s) { "a" => { return 1; }, "b" => { return 7; }, _ => { return 0; } } }`, 7},
		{"string-default", `function main(): i32 { let s: string = "z"; match (s) { "a" => { return 1; }, "b" => { return 7; }, _ => { return 4; } } }`, 4},
		{"guard-true", `function main(): i32 { let n: i32 = 5; match (n) { 1 => { return 1; }, 5 when n > 3 => { return 88; }, _ => { return 0; } } }`, 88},
		{"guard-false-falls-through", `function main(): i32 { let n: i32 = 5; match (n) { 1 => { return 1; }, 5 when n > 100 => { return 88; }, _ => { return 9; } } }`, 9},
		{"expr-i32", `function main(): i32 { let n: i32 = 2; let r: i32 = match (n) { 1 => 10, 2 => 20, _ => 0 }; return r; }`, 20},
		{"expr-default", `function main(): i32 { let n: i32 = 8; let r: i32 = match (n) { 1 => 10, 2 => 20, _ => 30 }; return r; }`, 30},
		{"single-literal-plus-default", `function main(): i32 { let n: i32 = 4; match (n) { 1 => { return 1; }, _ => { return 42; } } }`, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRunIR(t, tc.src); got != tc.want {
				t.Errorf("self-host IR %q: exit = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
