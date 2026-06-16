package e2e

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
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "flatten.fern", "checker.fern", "bundle_run_arm64.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "bundle_run_arm64.fern", "driver")

	lexerSrc, _ := os.ReadFile(filepath.Join(dir, "lexer.fern"))
	parserSrc, _ := os.ReadFile(filepath.Join(dir, "parser.fern"))
	checkerSrc, _ := os.ReadFile(filepath.Join(dir, "checker.fern"))
	utilSrc, _ := os.ReadFile(filepath.Join(dir, "util.fern"))
	ioSrc, err := os.ReadFile("../../internal/stdlib/std/io.fern")
	if err != nil {
		t.Fatalf("read std/io.fern: %v", err)
	}
	// The committed checker_run.fern driver, with its `import "std/io"`
	// retargeted to the bundled io module.
	runSrc, err := os.ReadFile("../../examples/self_host/checker_run.fern")
	if err != nil {
		t.Fatalf("read checker_run.fern: %v", err)
	}
	driverMod := strings.ReplaceAll(string(runSrc), "import \"std/io\";", "import \"./io\";")
	var bundle bytes.Buffer
	bundle.WriteString("///MODULE util\n")
	bundle.Write(utilSrc)
	bundle.WriteString("///MODULE lexer\n")
	bundle.Write(lexerSrc)
	bundle.WriteString("\n///MODULE parser\n")
	bundle.Write(parserSrc)
	bundle.WriteString("\n///MODULE checker\n")
	bundle.Write(checkerSrc)
	bundle.WriteString("\n///MODULE io\n")
	bundle.Write(ioSrc)
	bundle.WriteString("\n///MODULE main\n")
	bundle.WriteString(driverMod)

	checkerAsm := runCapture(t, x86gcc, x86runner, driverBin, bundle.Bytes())
	if len(checkerAsm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the checker driver")
	}
	checkerBin := buildBin(t, arm64gcc, dir, "checker", string(checkerAsm))

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
