package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
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
	{"range-sum", "import \"./math\";\nfunction main(): i32 { var r = math.range(0, 5); var s: i32 = 0; var i: i32 = 0; while (i < r.len()) { s = s + r[i]; i = i + 1; } return s; }\n", 10},
	{"range-step-sum", "import \"./math\";\nfunction main(): i32 { var r = math.range_step(0, 10, 2); var s: i32 = 0; var i: i32 = 0; while (i < r.len()) { s = s + r[i]; i = i + 1; } return s; }\n", 20},
	{"random-bytes-len", "import \"./math\";\nfunction main(): i32 { var b: string = random_bytes(7); return b.len(); }\n", 7},
}

// TestSelfHostMathX86_64 proves the self-hosted compiler compiles the
// real internal/stdlib/std/math.fern (which it couldn't before, lacking
// the random_bytes builtin) and the resulting binaries run correctly.
func TestSelfHostMathX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "bundle_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "bundle_run.fern", "driver")

	for _, tc := range mathCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, bytesModuleBundle(t, "math", tc.main))
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
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

// TestSelfHostMathArm64 is the ARM64 counterpart (CI-gated, qemu).
func TestSelfHostMathArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm_arm64.fern", "flatten.fern", "bundle_run_arm64.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "bundle_run_arm64.fern", "driver")

	for _, tc := range mathCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, bytesModuleBundle(t, "math", tc.main))
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
