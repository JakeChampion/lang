package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// exitIRCases exercise the `exit(n)` builtin through the IR path on all three
// backends. exit lowers to an inline sys_exit (x86 #60 / arm64 #93) or proc_exit
// (wasm) op — no runtime helper. The program's `return 99` is never reached, so
// the exit code proves exit ran with the right argument. These programs are
// heap-free, so the IR path emits ~1 KB vs the ~35 KB AST runtime — the size
// check confirms the IR path was taken (a silent AST fallback would be huge).
var exitIRCases = []struct {
	name string
	src  string
	want int
}{
	{"exit-basic", `function main(): i32 { exit(7); return 99; }`, 7},
	{"exit-in-if", `function main(): i32 { if (1 < 2) { exit(5); } return 99; }`, 5},
	{"exit-computed", `function main(): i32 { exit(3 + 4); return 99; }`, 7},
}

func TestSelfHostExitIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	for _, tc := range exitIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 18000 {
				t.Fatalf("%s: asm is %d bytes — expected the small IR output, not the AST runtime", tc.name, len(asm))
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostExitIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64.fern", "asm_arm64_ir.fern",
		"asm.fern", "asm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range exitIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64", "-ir")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64", "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 || len(asm) > 18000 {
				t.Fatalf("%s: driver asm is %d bytes (err %v) — expected the small IR output", tc.name, len(asm), err)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "ex_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exit arm64 IR %s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostExitIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host exit wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	for _, tc := range exitIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(wat, []byte("call $proc_exit")) {
				t.Fatalf("%s: no call $proc_exit in wat — did not lower through the wasm IR path", tc.name)
			}
			watFile := filepath.Join(dir, "ex_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: wasmtime did not exit normally", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exit wasm IR %s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
