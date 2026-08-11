package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostCheckerDriverX86_64 proves the self-hosted compiler can
// compile the self-hosted TYPE CHECKER: it bundles lexer + parser +
// checker + the real std/io + checker_run (the stdin driver), compiles
// that with the Go-built bundle_run, and runs the resulting binary —
// a self-hosted type checker — over well-typed and ill-typed programs,
// asserting it exits 0 / 1 respectively.
func TestSelfHostCheckerDriverX86_64(t *testing.T) {
	// Compile the self-hosted checker binary (checker_run, importing
	// std/io + ./lexer + ./parser + ./checker) with the file-based asm
	// driver via buildCheckerDriverBin — the loader resolves std/io to the
	// vendored flat io.fern, so no ///MODULE bundle / import rewrite needed.
	checkerBin, runner, _ := buildCheckerDriverBin(t, "checker_run.fern", false)

	cases := []struct {
		name     string
		src      string
		wantExit int
		// wantDiag, when non-empty, must appear on the driver's stderr — the
		// formatted diagnostic (bootstrap error-reporting via format_diags).
		wantDiag string
	}{
		{"well-typed-arith", "function main(): i32 { return 1 + 2; }\n", 0, ""},
		{"well-typed-vars", "function main(): i32 { var a: i32 = 5; var b: i32 = a + 1; return b; }\n", 0, ""},
		{"return-type-mismatch", "function main(): i32 { var s: string = \"x\"; return s; }\n", 1, "error[E002]"},
		{"arith-type-mismatch", "function main(): i32 { var s: string = \"x\"; return 1 + s; }\n", 1, "error["},
		// Immutability rejections surface a formatted diagnostic on stderr.
		{"field-assign-e048", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p.x = 5; return p.x; }\n", 1, "error[E048]"},
		{"subscript-assign-e056", "function main(): i32 { var a: i32[] = [1, 2, 3]; a[0] = 9; return a[0]; }\n", 1, "error[E056]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(checkerBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], checkerBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("%s: checker exited %d, want %d\nstderr: %s", tc.name, code, tc.wantExit, stderr.String())
			}
			if tc.wantDiag != "" && !strings.Contains(stderr.String(), tc.wantDiag) {
				t.Errorf("%s: stderr missing %q\ngot stderr: %s", tc.name, tc.wantDiag, stderr.String())
			}
		})
	}
}

// TestSelfHostCheckerDriverArm64 is the ARM64 counterpart (CI-gated,
// qemu-aarch64): the self-hosted aarch64 compiler compiles the type
// checker and the resulting binary type-checks programs under qemu.
func TestSelfHostCheckerDriverArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)

	// Compile checker_run (importing std/io + ./lexer + ./parser +
	// ./checker) with the arm64 file-based driver: the loader resolves
	// std/io to the vendored flat io.fern, so the source is unmodified.
	files := map[string]string{}
	for _, m := range []string{"util", "lexer", "parser", "checker"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", m+".fern"))
		if err != nil {
			t.Fatalf("read %s.fern: %v", m, err)
		}
		files[m+".fern"] = string(src)
	}
	ioSrc, err := os.ReadFile("../../internal/stdlib/std/io.fern")
	if err != nil {
		t.Fatalf("read std/io.fern: %v", err)
	}
	files["io.fern"] = string(ioSrc)
	runSrc, err := os.ReadFile("../../examples/self_host/checker_run.fern")
	if err != nil {
		t.Fatalf("read checker_run.fern: %v", err)
	}
	files["main.fern"] = string(runSrc)

	checkerAsm, progDir := compileFilesModload(t, x86runner, driverBin, files, "-target", "arm64-linux")
	if len(checkerAsm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the checker driver")
	}
	checkerBin := buildBin(t, arm64gcc, progDir, "checker", checkerAsm)

	cases := []struct {
		name     string
		src      string
		wantExit int
	}{
		{"well-typed", "function main(): i32 { return 1 + 2; }\n", 0},
		{"mismatch", "function main(): i32 { var s: string = \"x\"; return s; }\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runArm64Bin(qemu, checkerBin)
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("%s: checker exited %d, want %d", tc.name, code, tc.wantExit)
			}
		})
	}
}
