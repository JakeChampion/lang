package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"
)

var arrayMethodSyntaxCases = []struct {
	name string
	src  string
	exit int
}{
	{"sum_squared", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [5, 3, 8, 1]; return a.sum_squared(); }\n", 99},
	{"sum_abs", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [5, 3, 8, 1]; return a.sum_abs(); }\n", 17},
	{"reversed", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [5, 3, 8, 1]; let b: i32[] = a.reverse(); return b[0]; }\n", 1},
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
