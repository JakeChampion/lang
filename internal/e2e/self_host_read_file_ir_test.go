package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// readFileIRCases exercise the `read_file(path)` builtin through the IR path on
// x86-64 and arm64 (wasm has no file I/O, so wasm_eligible rejects read_file
// modules). read_file lowers to a value IR op that pops the path string box and
// calls each backend's __fern_read_file helper, pushing a fresh
// Result[string, IoError] box — so `match (read_file(p)) { Ok(s) => …, Err(e)
// => … }` lowers like any other Result (the Result type is recognised by
// opt_ret_type's read_file fallback).
//
// The harness writes "hello" (5 bytes, no newline) to rf_data.txt in the run
// directory. `len` returns the Ok contents' length (exercising the str-tracking
// that makes `.len()` dispatch to str_len); `echo` writes the contents; `missing`
// reads a non-existent file and takes the Err arm.
var readFileIRCases = []struct {
	name, src, wantOut string
	wantExit           int // used when wantOut == ""
}{
	{"len", `function main(): i32 { match (read_file("rf_data.txt")) { Ok(s) => { return s.len(); }, Err(e) => { return 99; } } return 0; }`, "", 5},
	{"echo", `function main(): i32 { match (read_file("rf_data.txt")) { Ok(s) => { write(s); return 0; }, Err(e) => { return 1; } } return 0; }`, "hello", 0},
	{"missing", `function main(): i32 { match (read_file("rf_nope.txt")) { Ok(s) => { return 0; }, Err(e) => { return 42; } } return 0; }`, "", 42},
	{"bind", `function main(): i32 { var r = read_file("rf_data.txt"); match (r) { Ok(s) => { return s.len(); }, Err(e) => { return 7; } } return 0; }`, "", 5},
}

func writeRFData(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "rf_data.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write rf_data.txt: %v", err)
	}
}

func TestSelfHostReadFileIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	writeRFData(t, dir)
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	for _, tc := range readFileIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fern_read_file")) {
				t.Fatalf("%s: no call to __fern_read_file — did not lower through the IR path", tc.name)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			cmd.Dir = dir
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

func TestSelfHostReadFileIRArm64(t *testing.T) {
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
	writeRFData(t, dir)
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_ir_run.fern", "driver")
	for _, tc := range readFileIRCases {
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
			if !bytes.Contains(asm, []byte("bl __fern_read_file")) {
				t.Fatalf("%s: no bl __fern_read_file — did not lower through the arm64 IR path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "rf_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			run.Dir = dir
			var stdout bytes.Buffer
			run.Stdout = &stdout
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally", tc.name)
			}
			if tc.wantOut != "" || tc.wantExit == 0 {
				if stdout.String() != tc.wantOut {
					t.Errorf("read_file arm64 %s: stdout %q, want %q", tc.name, stdout.String(), tc.wantOut)
				}
			}
			if tc.wantOut == "" && tc.wantExit != 0 {
				if code := run.ProcessState.ExitCode(); code != tc.wantExit {
					t.Errorf("read_file arm64 %s: exit %d, want %d", tc.name, code, tc.wantExit)
				}
			}
		})
	}
}
