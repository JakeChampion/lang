package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostForInCallResultIRX86_64 verifies that `for x in <call>()` — a
// for-in loop whose iterable is an array-returning function call — is admitted to
// the IR path on the register backends. The loop snapshots the call result into a
// hidden local (array-typed via arr_ret_fns / strarr_ret_fns) and iterates it;
// a register eligibility probe that lowers each function with EMPTY array
// registries does not see the snapshot's slot as an array, so the for-in bails
// and the whole module is wrongly deemed ineligible -> AST.
// all_eligible now uses ir_eligible_wide (the same registries
// emit_function_via_ir uses), so eligibility means "lowers" and these modules
// take the small IR path. (The wasm gate stays narrow — its array-call foreach
// lowering is a separate follow-up.)
//
// Two shapes: a free function returning i32[] (sum = 1+2+4 = 7) and one returning
// string[] (count = 3). Size checks prove the IR path was taken (the AST path
// pulled in a ~35 KB runtime); exit codes pin correctness.
func TestSelfHostForInCallResultIRX86_64(t *testing.T) {
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

	for _, tc := range []struct {
		name string
		prog string
		want int
	}{
		{"i32-array-call", `function mk(): i32[] { return [1, 2, 4]; }
function f(): i32 { var s: i32 = 0; for x in mk() { s = s + x; } return s; }
function main(): i32 { return f(); }`, 7},
		{"string-array-call", `function getkeys(): string[] { return ["a", "b", "c"]; }
function f(): i32 { var c: i32 = 0; for k in getkeys() { c = c + 1; } return c; }
function main(): i32 { return f(); }`, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.prog))
			if len(asm) == 0 || len(asm) > 18000 {
				t.Fatalf("asm is %d bytes — expected small IR output; the for-in-over-call module likely bailed to the AST runtime", len(asm))
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
