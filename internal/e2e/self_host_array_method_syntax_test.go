package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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
	{"reversed", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [5, 3, 8, 1]; var b: i32[] = a.reversed(); return b[0]; }\n", 1},
	// sorted_asc likewise returns i32[].
	{"sorted_asc", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [3, 1, 2]; var s: i32[] = a.sorted_asc(); return s[0] * 100 + s[1] * 10 + s[2]; }\n", 123},
}

func TestSelfHostArrayMethodSyntaxX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t) // lexer, parser, asm
	for _, name := range []string{"flatten.fern", "util.fern", "bundle_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "bundle_run.fern", "driver")

	for _, tc := range arrayMethodSyntaxCases {
		t.Run(tc.name, func(t *testing.T) {
			bundle := selfHostBundleFor(t, tc.src)
			asm := runCapture(t, gcc, runner, driverBin, bundle)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "ams_"+tc.name, string(asm))
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
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "flatten.fern", "bundle_run_arm64.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "bundle_run_arm64.fern", "driver")

	for _, tc := range arrayMethodSyntaxCases {
		t.Run(tc.name, func(t *testing.T) {
			bundle := selfHostBundleFor(t, tc.src)
			asm := runCapture(t, x86gcc, x86runner, driverBin, bundle)
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "ams_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s via method syntax exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
