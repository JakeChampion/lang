package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostForInCallResultIRX86_64 verifies that `for x in <call>()` — a
// for-in loop whose iterable is an array-returning function call — lowers on the
// register backends. The loop snapshots the call result into a hidden local and
// iterates it, so that local has to be typed as an array.
//
// Two shapes: a free function returning i32[] (sum = 1+2+4 = 7) and one returning
// string[] (count = 3). Exit codes pin correctness.
func TestSelfHostForInCallResultIRX86_64(t *testing.T) {
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

	for _, tc := range []struct {
		name string
		prog string
		want int
	}{
		{"i32-array-call", `function mk(): i32[] { return [1, 2, 4]; }
function f(): i32 { let s: i32 = 0; for x in mk() { s = s + x; } return s; }
function main(): i32 { return f(); }`, 7},
		{"string-array-call", `function getkeys(): string[] { return ["a", "b", "c"]; }
function f(): i32 { let c: i32 = 0; for k in getkeys() { c = c + 1; } return c; }
function main(): i32 { return f(); }`, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.prog))
			if len(asm) == 0 {
				t.Fatal("driver produced no asm")
			}
			progBin := buildBin(t, gcc, dir, "forin_call_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exit %d, want %d", code, tc.want)
			}
		})
	}
}
