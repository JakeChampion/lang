package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// readLineIRCases exercise the `read_line()` builtin through the IR path on
// x86-64 and arm64 (wasm has no stdin, so wasm_eligible rejects read_line
// modules). read_line lowers to a value IR op that calls each backend's
// __fern_read_line helper, returning a fresh string box (the line without its
// trailing newline). `wantOut` cases echo it with write and check stdout;
// `wantExit` cases return read_line().len() (exercising the str-tracking that
// makes `.len()` dispatch to str_len) and check the exit code.
var readLineIRCases = []struct {
	name, src, stdin, wantOut string
	wantExit                  int // used when wantOut == ""
}{
	{"echo", `function main(): i32 { var s: string = read_line(); write(s); return 0; }`, "hello\nrest", "hello", 0},
	{"echo-no-newline", `function main(): i32 { var s: string = read_line(); write(s); return 0; }`, "bare", "bare", 0},
	{"empty-line", `function main(): i32 { var s: string = read_line(); write(s); return 0; }`, "\nx", "", 0},
	{"len", `function main(): i32 { var s: string = read_line(); return s.len(); }`, "abcd\n", "", 4},
}

func TestSelfHostReadLineIRX86_64(t *testing.T) {
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
	for _, tc := range readLineIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fern_read_line")) {
				t.Fatalf("%s: no call to __fern_read_line — did not lower through the IR path", tc.name)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.stdin))
			var stdout bytes.Buffer
			cmd.Stdout = &stdout
			_ = cmd.Run()
			if tc.wantOut != "" || tc.wantExit == 0 {
				if stdout.String() != tc.wantOut {
					t.Errorf("%s: stdout %q, want %q", tc.name, stdout.String(), tc.wantOut)
				}
			}
			if tc.wantOut == "" && tc.wantExit != 0 {
				if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
					t.Errorf("%s: exit %d, want %d", tc.name, code, tc.wantExit)
				}
			}
		})
	}
}

func TestSelfHostReadLineIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64.fern", "asm_arm64_ir.fern",
		"asm_arm64_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_ir_run.fern", "driver")
	for _, tc := range readLineIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(asm, []byte("bl __fern_read_line")) {
				t.Fatalf("%s: no bl __fern_read_line — did not lower through the arm64 IR path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "rl_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			run.Stdin = bytes.NewReader([]byte(tc.stdin))
			var stdout bytes.Buffer
			run.Stdout = &stdout
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally", tc.name)
			}
			if tc.wantOut != "" || tc.wantExit == 0 {
				if stdout.String() != tc.wantOut {
					t.Errorf("read_line arm64 %s: stdout %q, want %q", tc.name, stdout.String(), tc.wantOut)
				}
			}
			if tc.wantOut == "" && tc.wantExit != 0 {
				if code := run.ProcessState.ExitCode(); code != tc.wantExit {
					t.Errorf("read_line arm64 %s: exit %d, want %d", tc.name, code, tc.wantExit)
				}
			}
		})
	}
}
