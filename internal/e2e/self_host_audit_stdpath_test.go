package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// auditStdPathCases compile the real std/path (import-free) prepended to a
// main, through the self-hosted compiler, asserting the exit code.
// Self-host arm of the §D std/path audit (docs/FEATURE-AUDIT.md); the
// native arm is the `audit_std_path_numeric` fixture (all four backends).
var auditStdPathCases = []struct {
	name string
	main string
	exit int
}{
	{"join", `function main(): i32 { if (path_join(["a", "b", "c"]) == "a/b/c") { return 7; } return 1; }`, 7},
	{"file-name", `function main(): i32 { if (path_file_name("/x/y/z.txt") == "z.txt") { return 8; } return 1; }`, 8},
	{"extension", `function main(): i32 { if (path_extension("z.txt") == "txt") { return 9; } return 1; }`, 9},
}

func stdPathSource(t *testing.T, mainBody string) []byte {
	t.Helper()
	src, err := os.ReadFile("../../internal/stdlib/std/path.fern")
	if err != nil {
		t.Fatalf("read std/path.fern: %v", err)
	}
	return append(src, []byte("\n"+mainBody+"\n")...)
}

// TestSelfHostAuditStdPathX86_64 compiles std/path + a main through the
// self-hosted x86-64 driver and asserts the exit code.
func TestSelfHostAuditStdPathX86_64(t *testing.T) {
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

	for _, tc := range auditStdPathCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, stdPathSource(t, tc.main))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "path_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
