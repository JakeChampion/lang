package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// charMethodCases exercise the inline ASCII i32 methods (to_lower/upper,
// is_digit/alpha/alnum/lower/upper/hex_digit) — the std/i32 char
// classifiers std/sort and std/string call. Cross-checked vs Go.
var charMethodCases = []struct {
	name string
	src  string
	exit int
}{
	{"to_lower", "function main(): i32 { var c: i32 = 65; return c.to_lower(); }", 97},
	{"to_lower-noop", "function main(): i32 { var c: i32 = 53; return c.to_lower(); }", 53},
	{"to_upper", "function main(): i32 { var c: i32 = 122; return c.to_upper(); }", 90},
	{"is_digit", "function main(): i32 { var c: i32 = 53; if (c.is_digit()) { return 1; } return 0; }", 1},
	{"is_digit-false", "function main(): i32 { var c: i32 = 65; if (c.is_digit()) { return 1; } return 0; }", 0},
	{"is_alpha-upper", "function main(): i32 { var c: i32 = 90; if (c.is_alpha() && c.is_upper() && !c.is_lower()) { return 1; } return 0; }", 1},
	{"is_hex-alnum", "function main(): i32 { var c: i32 = 102; if (c.is_hex_digit() && c.is_alnum()) { return 1; } return 0; }", 1},
	{"punct-neither", "function main(): i32 { var c: i32 = 35; if (c.is_alnum() || c.is_hex_digit()) { return 1; } return 0; }", 0},
	{"gcd", "function main(): i32 { var a: i32 = 12; return a.gcd(18); }", 6},
	{"lcm", "function main(): i32 { var a: i32 = 4; return a.lcm(6); }", 12},
	{"gcd-neg", "function main(): i32 { var a: i32 = 0 - 12; return a.gcd(8); }", 4},
	{"to_ascii_string", "function main(): i32 { var c: i32 = 65; return c.to_ascii_string()[0]; }", 65},
}

// TestSelfHostCharMethodsX86_64 compiles the char-method programs with
// the self-hosted compiler and checks exit codes.
func TestSelfHostCharMethodsX86_64(t *testing.T) {
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

	for _, tc := range charMethodCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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

// TestSelfHostSortX86_64 proves the self-hosted compiler compiles the
// real std/sort (which needed i32.to_lower) and the result sorts. Exercises
// the `own`-consuming `sort_i32_inplace_asc` — one of std/sort's remaining
// monomorphic sorts after the per-width zoo retired to core/cmp (#5397).
func TestSelfHostSortX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	main := "import \"./sort\";\n" +
		"function main(): i32 { var r = sort.sort_i32_inplace_asc([5, 2, 8, 1, 9, 3]); return r[0] * 100 + r[5]; }\n"
	asm, progDir := compileStdProgModload(t, runner, driverBin, []string{"sort"}, main)
	progBin := buildBin(t, gcc, progDir, "sortprog", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 109 { // min 1 * 100 + max 9
		t.Errorf("sort_i32_inplace_asc result exited %d, want 109 (min=1, max=9)", code)
	}
}

// TestSelfHostCharMethodsArm64 — CI-gated arm64 counterpart.
func TestSelfHostCharMethodsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range charMethodCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostSortArm64 — CI-gated arm64 counterpart.
func TestSelfHostSortArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)
	main := "import \"./sort\";\n" +
		"function main(): i32 { var r = sort.sort_i32_inplace_asc([5, 2, 8, 1, 9, 3]); return r[0] * 100 + r[5]; }\n"
	asm, progDir := compileStdProgModload(t, x86runner, driverBin, []string{"sort"}, main, "-target", "arm64")
	progBin := buildBin(t, arm64gcc, progDir, "sortprog", asm)
	cmd := runArm64Bin(qemu, progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 109 {
		t.Errorf("sort_i32_inplace_asc result exited %d, want 109", code)
	}
}
