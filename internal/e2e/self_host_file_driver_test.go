package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostFileDriverX86_64 exercises asm_file_run.fern — the
// file-driven self-host compiler driver. Unlike asm_run (stdin), it
// reads the source FILE named in argv[1] via args() + read_file and
// prints its x86-64 asm. The test:
//
//   - builds the driver with the Go backend,
//   - compiles several programs BY FILE PATH, assembling + running the
//     emitted binaries and asserting their exit codes,
//   - checks the no-arg (exit 2) and missing-file (exit 1) paths,
//   - and proves the driver is SELF-HOSTABLE: rebuilt through the
//     bundle pipeline by the self-hosted compiler, it produces
//     byte-identical asm for the same input file.
func TestSelfHostFileDriverX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		// The driver takes a host filesystem path as argv[1]; a qemu
		// runner wouldn't see the same path. Native-only.
		t.Skip("file-driven driver test runs only natively (argv path)")
	}
	dir := writeSelfHostAsmProject(t) // lexer.fern, parser.fern, asm.fern
	for _, name := range []string{"asm_file_run.fern", "bundle_run.fern", "flatten.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Build asm_file_run with the Go backend.
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_file_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"return-42", "function main(): i32 { return 42; }\n", 42},
		{"arith", "function main(): i32 { return 6 * 7; }\n", 42},
		{"locals", "function main(): i32 { var x = 19; var y = 23; return x + y; }\n", 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srcPath := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(srcPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", srcPath, err)
			}
			asm, err := exec.Command(driverBin, srcPath).Output()
			if err != nil {
				t.Fatalf("driver on %s: %v", srcPath, err)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("file-compiled %s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}

	t.Run("no-arg-exits-2", func(t *testing.T) {
		cmd := exec.Command(driverBin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 2 {
			t.Errorf("no-arg driver exited %d, want 2", code)
		}
	})
	t.Run("missing-file-exits-1", func(t *testing.T) {
		cmd := exec.Command(driverBin, filepath.Join(dir, "nope.fern"))
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 1 {
			t.Errorf("missing-file driver exited %d, want 1", code)
		}
	})

	// Self-hostability: rebuild asm_file_run through the bundle
	// pipeline (lexer+parser+asm+flatten+driver) with the Go-built
	// bundle_run, producing the SELF-HOSTED driver, and assert it emits
	// byte-identical asm to the Go-built driver for the same input.
	t.Run("self-hostable", func(t *testing.T) {
		bundleRunBin := buildSelfHostBin(t, gcc, dir, "bundle_run.fern", "bundle_run")
		lexerSrc, _ := os.ReadFile(filepath.Join(dir, "lexer.fern"))
		parserSrc, _ := os.ReadFile(filepath.Join(dir, "parser.fern"))
		asmSrc, _ := os.ReadFile(filepath.Join(dir, "asm.fern"))
		driverSrc, _ := os.ReadFile(filepath.Join(dir, "asm_file_run.fern"))
		var bundle bytes.Buffer
		bundle.WriteString("///MODULE lexer\n")
		bundle.Write(lexerSrc)
		bundle.WriteString("\n///MODULE parser\n")
		bundle.Write(parserSrc)
		bundle.WriteString("\n///MODULE asm\n")
		bundle.Write(asmSrc)
		bundle.WriteString("\n///MODULE main\n")
		bundle.Write(driverSrc)
		shDriverAsm := runCapture(t, gcc, runner, bundleRunBin, bundle.Bytes())
		shDriverBin := buildBin(t, gcc, dir, "asm_file_run_sh", string(shDriverAsm))

		srcPath := filepath.Join(dir, "fixpoint_input.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { return 42; }\n"), 0o644); err != nil {
			t.Fatalf("write input: %v", err)
		}
		goAsm, err := exec.Command(driverBin, srcPath).Output()
		if err != nil {
			t.Fatalf("go-built driver: %v", err)
		}
		shAsm, err := exec.Command(shDriverBin, srcPath).Output()
		if err != nil {
			t.Fatalf("self-host-built driver: %v", err)
		}
		if !bytes.Equal(goAsm, shAsm) {
			t.Errorf("self-host-built driver output differs from Go-built: %d vs %d bytes", len(shAsm), len(goAsm))
		}
		// And the emitted program must actually work.
		progBin := buildBin(t, gcc, dir, "fixpoint_prog", string(shAsm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("self-host-driver-compiled program exited %d, want 42", code)
		}
	})
}

// buildSelfHostBin loads a self-host driver .fern (by file name in dir),
// compiles it with the Go x86-64 backend, and links it into dir/out.
func buildSelfHostBin(t *testing.T, gcc, dir, fernName, out string) string {
	t.Helper()
	prog, _, err := modload.Load(filepath.Join(dir, fernName))
	if err != nil {
		t.Fatalf("modload %s: %v", fernName, err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold %s: %v", fernName, err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check %s: %v", fernName, err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit %s: %v", fernName, err)
	}
	return buildBin(t, gcc, dir, out, asm)
}
