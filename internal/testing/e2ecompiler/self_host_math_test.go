package e2ecompiler

import (
	"os/exec"
	"testing"
)

// mathCases compile the real std/math with the self-hosted compiler.
// math needed only the random_bytes builtin (its float helpers aside);
// these exercise deterministic functions plus random_bytes' length.
var mathCases = []struct {
	name string
	main string
	exit int
}{
	{"range-sum", "import \"std/math\";\nfunction main(): i32 { let r = math.range(0, 5); let s: i32 = 0; let i: i32 = 0; while (i < r.len()) { s = s + r[i]; i = i + 1; } return s; }\n", 10},
	{"range-step-sum", "import \"std/math\";\nfunction main(): i32 { let r = math.range_step(0, 10, 2); let s: i32 = 0; let i: i32 = 0; while (i < r.len()) { s = s + r[i]; i = i + 1; } return s; }\n", 20},
	{"random-bytes-len", "import \"std/math\";\nfunction main(): i32 { let b: u8[] = random_bytes(7); return b.len(); }\n", 7},
}

// TestSelfHostMathX86_64 proves the self-hosted compiler compiles the
// real internal/stdlib/std/math.fern (which it couldn't before, lacking
// the random_bytes builtin) and the resulting binaries run correctly.
func TestSelfHostMathX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	for _, tc := range mathCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, progDir := compileSourceModload(t, runner, driverBin, tc.main)
			progBin := buildBin(t, gcc, progDir, tc.name, asm)
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

// TestSelfHostMathArm64 is the ARM64 counterpart (CI-gated, qemu),
// compiled via the file-based driver (asm_modload_run -target arm64-linux).
func TestSelfHostMathArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)

	for _, tc := range mathCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, progDir := compileSourceModload(t, x86runner, driverBin, tc.main, "-target", "arm64-linux")
			progBin := buildBin(t, arm64gcc, progDir, tc.name, asm)
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
