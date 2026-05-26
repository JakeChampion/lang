package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// stringCases bundle the full std/string module (its imports are
// prelude-resident: core/int, std/i32, std/array) plus a main, and
// check the exit code. std/string links on the self-host once
// __memcpy + s.as_bytes() resolve (its bytes() body, which the
// self-host instead dispatches to __fern_str_bytes). Exit codes
// cross-checked vs the Go backend.
var stringCases = []struct {
	name string
	main string
	exit int
}{
	{"index-of", `return "hello world".index_of("world");`, 6},
	{"trim-len", `return "  hi  ".trim().len();`, 2},
	{"to-upper", `var u: string = "abc".to_upper(); return u[0];`, 65},
	{"to-lower", `var u: string = "ABC".to_lower(); return u[0];`, 97},
	{"contains", `if ("hello".contains("ell")) { return 7; } return 0;`, 7},
	{"starts-with", `if ("hello".starts_with("he")) { return 7; } return 0;`, 7},
	{"replace-len", `return "a.b.c".replace(".", "-").len();`, 5},
	{"repeat-len", `return "ab".repeat(3).len();`, 6},
	{"split-count", `return "a,b,c".split(",").len();`, 3},
}

func stringSource(t *testing.T, mainBody string) []byte {
	t.Helper()
	src, err := os.ReadFile("../../internal/stdlib/std/string.fern")
	if err != nil {
		t.Fatalf("read std/string.fern: %v", err)
	}
	return append(src, []byte("\nfunction main(): i32 { "+mainBody+" }\n")...)
}

// TestSelfHostStringX86_64 compiles std/string + a main with the
// self-hosted x86-64 compiler and checks exit codes.
func TestSelfHostStringX86_64(t *testing.T) {
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

	for _, tc := range stringCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, stringSource(t, tc.main))
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
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostStringArm64 — CI-gated arm64 counterpart.
func TestSelfHostStringArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm_arm64.fern", "asm_arm64_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_run.fern", "driver")

	for _, tc := range stringCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, stringSource(t, tc.main))
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
