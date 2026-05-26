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
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t) // lexer, parser, asm
	for _, name := range []string{"flatten.fern", "checker.fern", "bundle_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "bundle_run.fern", "driver")

	// Bundle: lexer + parser + checker + the unmodified std/io as the
	// `io` module + a checker driver whose `import "std/io"` is
	// retargeted to it. The driver reads stdin, type-checks, exits 0/1.
	lexerSrc, _ := os.ReadFile(filepath.Join(dir, "lexer.fern"))
	parserSrc, _ := os.ReadFile(filepath.Join(dir, "parser.fern"))
	checkerSrc, _ := os.ReadFile(filepath.Join(dir, "checker.fern"))
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

	checkerAsm := runCapture(t, gcc, runner, driverBin, bundle.Bytes())
	if len(checkerAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the checker driver")
	}
	checkerBin := buildBin(t, gcc, dir, "checker", string(checkerAsm))

	cases := []struct {
		name     string
		src      string
		wantExit int
	}{
		{"well-typed-arith", "function main(): i32 { return 1 + 2; }\n", 0},
		{"well-typed-vars", "function main(): i32 { var a: i32 = 5; var b: i32 = a + 1; return b; }\n", 0},
		{"return-type-mismatch", "function main(): i32 { var s: string = \"x\"; return s; }\n", 1},
		{"arith-type-mismatch", "function main(): i32 { var s: string = \"x\"; return 1 + s; }\n", 1},
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
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("%s: checker exited %d, want %d", tc.name, code, tc.wantExit)
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
	for _, name := range []string{"lexer.fern", "parser.fern", "asm_arm64.fern", "flatten.fern", "checker.fern", "bundle_run_arm64.fern"} {
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
