package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
	src, err := os.ReadFile("../../examples/self_host/asm_file_run.fern")
	if err != nil {
		t.Fatalf("read asm_file_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_file_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_file_run.fern: %v", err)
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

	// (The former "self-hostable" subtest rebuilt asm_file_run through the
	// bundle_run ///MODULE pipeline and asserted byte-identical output.
	// bundle_run is retired; that file-driver-self-hosts-byte-identically
	// property is now covered by TestSelfHostModloadFixpointX86_64, which
	// self-hosts the asm_modload_run file driver to a 3-generation fixpoint.)
}

// buildSelfHostBin loads a self-host driver .fern (by file name in dir),
// compiles it with the Go x86-64 backend, and links it into dir/out.
// The source→asm compile is cached process-wide by source-set hash
// (see self_host_buildcache_test.go), so building the same driver in a
// later test reuses the emitted asm instead of recompiling 35k lines.
func buildSelfHostBin(t *testing.T, gcc, dir, fernName, out string) string {
	t.Helper()
	asm := cachedSelfHostAsm(t, dir, fernName)
	return buildBin(t, gcc, dir, out, asm)
}
