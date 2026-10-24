package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var lambdaStructFieldIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"one-field", `struct Box { f: (i32) => i32 } function main(): i32 { let b = Box { f: (x: i32): i32 => { return x + 1; } }; return (b.f)(10); }`, 11},
	{"two-fields", `struct Ops { inc: (i32) => i32, dbl: (i32) => i32 } function main(): i32 { let o = Ops { inc: (x: i32): i32 => { return x + 1; }, dbl: (x: i32): i32 => { return x * 2; } }; return (o.inc)(10) + (o.dbl)(10); }`, 31},
	{"mixed-scalar-and-fn", `struct Wrap { n: i32, f: (i32) => i32 } function main(): i32 { let w = Wrap { n: 5, f: (x: i32): i32 => { return x * 3; } }; return w.n + (w.f)(10); }`, 35},
	{"capturing-field", `struct Box { f: (i32) => i32 } function main(): i32 { let n = 10; let b = Box { f: (x: i32): i32 => { return x + n; } }; return (b.f)(7); }`, 17},
	{"capturing-two-caps", `struct Box { f: (i32) => i32 } function main(): i32 { let a = 8; let b = 100; let bx = Box { f: (x: i32): i32 => { return x + a + b; } }; return (bx.f)(0); }`, 108},
	{"capturing-mixed-field", `struct Wrap { n: i32, f: (i32) => i32 } function main(): i32 { let k = 3; let w = Wrap { n: 5, f: (x: i32): i32 => { return x * k; } }; return w.n + (w.f)(10); }`, 35},
}

func TestSelfHostLambdaStructFieldX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)

	probeSrc, err := os.ReadFile("../../../compiler/drivers/asm_pathprobe_run.fern")
	if err != nil {
		t.Fatalf("read asm_pathprobe_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_pathprobe_run.fern"), probeSrc, 0o644); err != nil {
		t.Fatalf("write asm_pathprobe_run.fern: %v", err)
	}
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	emit := func(t *testing.T, src string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		return string(out)
	}
	run := func(t *testing.T, asmText string) int {
		t.Helper()
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, []byte(asmText), 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, asmText)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally")
		}
		return inner.ProcessState.ExitCode()
	}

	for _, tc := range lambdaStructFieldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if route != "ir" {
				t.Errorf("%s routed through %q path, want \"ir\"", tc.name, route)
			}
			if got := run(t, emit(t, tc.src)); got != tc.expected {
				t.Errorf("lambda-structfield x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
