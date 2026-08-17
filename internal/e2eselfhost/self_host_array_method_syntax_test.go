package e2eselfhost

import (
	"bytes"
	"os/exec"
	"testing"
)

// arrayMethodSyntaxCases exercise std/array methods via METHOD syntax
// (`xs.sum_squared()`) rather than the explicit free-call form
// (`array.__method_Array_sum_squared(xs)`). Method syntax must desugar to the
// same `__method_Array_<name>` helper; before that dispatch existed, a
// non-codegen-intercepted array method fell through to the -1 sentinel and the
// program exited 255. [5,3,8,1]: sum_squared = 25+9+64+1 = 99.
var arrayMethodSyntaxCases = []struct {
	name string
	src  string
	exit int
}{
	{"sum_squared", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [5, 3, 8, 1]; return a.sum_squared(); }\n", 99},
	{"sum_abs", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [5, 3, 8, 1]; return a.sum_abs(); }\n", 17},
	// reversed returns a fresh i32[]; index it back to a scalar exit code.
	{"reversed", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [5, 3, 8, 1]; var b: i32[] = a.reverse(); return b[0]; }\n", 1},
	// sorted_asc likewise returns i32[].
	{"sorted_asc", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [3, 1, 2]; var s: i32[] = a.sorted_asc(); return s[0] * 100 + s[1] * 10 + s[2]; }\n", 123},
}

func TestSelfHostArrayMethodSyntaxX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	for _, tc := range arrayMethodSyntaxCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, progDir := compileSourceModload(t, runner, driverBin, tc.src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, progDir, "ams_"+tc.name, asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			cmd.Stdin = bytes.NewReader(nil)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s via method syntax exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostArrayMethodSyntaxArm64 is the arm64 counterpart: the self-host
// arm64 backend (asm_arm64.fern) must lower array method syntax to the same
// __method_Array_ helper. Built by the x86 self-host compiler (the arm64 bundle
// driver), linked + run under qemu.
func TestSelfHostArrayMethodSyntaxArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)

	for _, tc := range arrayMethodSyntaxCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, progDir := compileSourceModload(t, x86runner, driverBin, tc.src, "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, progDir, "ams_"+tc.name, asm)
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s via method syntax exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
