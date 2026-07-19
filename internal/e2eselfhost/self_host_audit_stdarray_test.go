package e2eselfhost

import (
	"os/exec"
	"testing"
)

// auditStdArrayCases broaden the self-host std/array coverage beyond the
// single gcd_all case in self_host_array_test.go: each compiles the real
// std/array bundle with a different reduction `main` through the self-hosted
// compiler and asserts the exit code. The FULL transitive closure is resolved
// (via compileSourceModload) so array's cmp-delegating methods — `sorted_asc` /
// `sorted_desc` route to core/cmp's generic `sort` / `sort_desc` (#5397) — pull
// core/cmp into the bundle and its generic bodies monomorphise correctly.
// Self-host arm of the §D std/array audit (docs/FEATURE-AUDIT.md); the native
// arm is the `audit_std_numeric` fixture (all four native backends).
var auditStdArrayCases = []struct {
	name string
	main string
	exit int
}{
	{"sum", `import "std/array";
function main(): i32 { var xs: i32[] = [3, 1, 2]; return array.__method_Array_sum(xs); }`, 6},
	{"product", `import "std/array";
function main(): i32 { var xs: i32[] = [3, 1, 2]; return array.__method_Array_product(xs); }`, 6},
	{"sorted_asc", `import "std/array";
function main(): i32 { var xs: i32[] = [3, 1, 2]; var s: i32[] = array.__method_Array_sorted_asc(xs); return s[0] * 100 + s[1] * 10 + s[2]; }`, 123},
	// base-5 positional encode of the descending result [3,2,1] (values < 5),
	// kept < 256 so it round-trips through the 8-bit exit code: 3*25+2*5+1 = 86.
	{"sorted_desc", `import "std/array";
function main(): i32 { var xs: i32[] = [1, 3, 2]; var s: i32[] = array.__method_Array_sorted_desc(xs); return s[0] * 25 + s[1] * 5 + s[2]; }`, 86},
	{"max-some", `import "std/array";
function main(): i32 { var xs: i32[] = [3, 1, 2]; match (array.__method_Array_max(xs)) { Some(m) => { return m; }, None => { return 0; } } }`, 3},
}

// TestSelfHostAuditStdArrayX86_64 compiles each std/array reduction bundle
// through the self-hosted x86-64 driver and asserts the exit code.
func TestSelfHostAuditStdArrayX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	for _, tc := range auditStdArrayCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, progDir := compileSourceModload(t, runner, driverBin, tc.main)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, progDir, "arr_"+tc.name, asm)
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
