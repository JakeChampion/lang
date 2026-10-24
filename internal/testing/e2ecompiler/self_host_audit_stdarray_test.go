package e2ecompiler

import (
	"os/exec"
	"testing"
)

var auditStdArrayCases = []struct {
	name string
	main string
	exit int
}{
	{"sum", `import "std/array";
function main(): i32 { let xs: i32[] = [3, 1, 2]; return xs.sum(); }`, 6},
	{"product", `import "std/array";
function main(): i32 { let xs: i32[] = [3, 1, 2]; return xs.product(); }`, 6},
	{"max-some", `import "std/array";
function main(): i32 { let xs: i32[] = [3, 1, 2]; match (xs.max()) { Some(m) => { return m; }, None => { return 0; } } }`, 3},
}

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
