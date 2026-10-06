package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tryOpCases cover the `?` try operator: `expr?` unwraps a Some/Ok
// payload, or early-returns the None/Err box from the enclosing
// Option/Result-returning function. The parser desugars it to the unary
// op "try_". Exit codes cross-checked vs the Go backend.
const tryDivHelper = "function checked_div(a: i32, b: i32): Option[i32] { if (b == 0) { return None; } return Some(a / b); } "

var tryOpCases = []struct {
	name string
	src  string
	exit int
}{
	{"chain-some", tryDivHelper + "function compute(): Option[i32] { let x: i32 = checked_div(84, 2)?; let y: i32 = checked_div(x, 1)?; return Some(y); } function main(): i32 { match (compute()) { Some(n) => { return n; }, None => { return 1; } } }", 42},
	{"propagate-none", tryDivHelper + "function compute(): Option[i32] { let x: i32 = checked_div(10, 0)?; return Some(x); } function main(): i32 { match (compute()) { Some(n) => { return n; }, None => { return 7; } } }", 7},
	{"second-none", tryDivHelper + "function compute(): Option[i32] { let x: i32 = checked_div(40, 2)?; let y: i32 = checked_div(x, 0)?; return Some(y); } function main(): i32 { match (compute()) { Some(n) => { return n; }, None => { return 9; } } }", 9},
}

// TestSelfHostTryOpX86_64 — the `?` operator with the self-hosted
// x86-64 compiler.
func TestSelfHostTryOpX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../compiler/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range tryOpCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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

// TestSelfHostTryOpArm64 — CI-gated arm64 counterpart.
func TestSelfHostTryOpArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tryOpCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}
