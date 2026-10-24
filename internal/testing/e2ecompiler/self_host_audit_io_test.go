package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var auditIOCases = []struct {
	name string
	src  string
	out  string
	exit int
}{
	{"print", `function main(): i32 { print("hello"); return 0; }`, "hello\n", 0},
	{"write-raw", `function main(): i32 { write("ab"); write("cd"); return 0; }`, "abcd", 0},
	{"eprint-not-stdout", `function main(): i32 { eprint("err"); print("out"); return 0; }`, "out\n", 0},
	{"exit-code", `function main(): i32 { exit(42); return 0; }`, "", 42},
}

func TestSelfHostAuditIOX86_64(t *testing.T) {
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

	for _, tc := range auditIOCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			out, _ := cmd.Output()
			if string(out) != tc.out {
				t.Errorf("%s stdout = %q, want %q", tc.name, string(out), tc.out)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
