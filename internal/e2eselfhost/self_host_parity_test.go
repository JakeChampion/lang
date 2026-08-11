package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The data-driven PARITY CORPUS (#4398 part 2). Every .fern under
// testdata/parity/ is a self-contained program (no imports, no stdin) that is
// run on the native interpreter (the ORACLE) and through each self-hosted IR
// backend — x86-64, arm64, wasm — asserting identical stdout + exit code.
// "Every feature ships with tests on every backend" is thereby a one-file
// cost: drop a program into testdata/parity/ and all three backend legs (plus
// the native oracle) pick it up. This replaces the old pattern of three
// near-identical Go files per feature (X_ir_test.go + X_arm64_ir_test.go +
// X_wasm_ir_test.go); the putchar (#2839), mut-capture (#2850 / SH-057), and
// u64-unsigned (#2904) triples were the first migrations.
//
// Corpus contract:
//   - native-valid: the program runs under `fern -interp` (that run IS the
//     expected behaviour — no expectations are hardcoded here);
//   - IR-eligible: every self-host driver takes the IR path, never the silent
//     AST fallback. The x86-64 leg enforces this per fixture via the driver's
//     `-ir-probe` report ("module: IR"), so an eligibility regression fails
//     loudly instead of quietly testing the legacy AST backend;
//   - deterministic stdout and an exit code <= 125 (WASI proc_exit range);
//   - no stdin, no argv, no filesystem/network.

// parityFixtures returns the corpus programs, sorted for stable subtest order.
func parityFixtures(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "parity", "*.fern"))
	if err != nil {
		t.Fatalf("glob parity corpus: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("parity corpus is empty (testdata/parity/*.fern)")
	}
	sort.Strings(paths)
	return paths
}

// runParityDriver pipes src into a self-host driver (wrapped in the host
// runner when cross-executing) and returns its stdout, failing the test on a
// non-zero exit or empty output.
func runParityDriver(t *testing.T, runner []string, driver, src string, args ...string) string {
	t.Helper()
	argv := append([]string{driver}, args...)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(argv[0], argv[1:]...)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), argv...)...)
	}
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		t.Fatalf("driver %v failed: %v (%d bytes out)", args, err, len(out))
	}
	return string(out)
}

// parityCase is one fixture plus its oracle verdict (the native interpreter's
// stdout + exit code).
type parityCase struct {
	name, src, wantOut string
	wantExit           int
}

// parityCases loads every fixture and runs the native-interp oracle on it.
func parityCases(t *testing.T) []parityCase {
	t.Helper()
	var cases []parityCase
	for _, fx := range parityFixtures(t) {
		src, err := os.ReadFile(fx)
		if err != nil {
			t.Fatalf("read %s: %v", fx, err)
		}
		wantOut, wantExit := runFixtureInterp(t, fx, "")
		cases = append(cases, parityCase{
			name:     strings.TrimSuffix(filepath.Base(fx), ".fern"),
			src:      string(src),
			wantOut:  wantOut,
			wantExit: wantExit,
		})
	}
	return cases
}

// TestSelfHostParityCorpusX86_64IR is the x86-64 leg: asm_ir_run emits each
// fixture via the IR path, the binary's stdout + exit code must match the
// native-interp oracle. This leg also gates ROUTING for the whole corpus:
// `-ir-probe` must report "module: IR" for every fixture, so none of the legs
// (this one, arm64, wasm — which share the asm_ir eligibility core) can pass
// by silently falling back to the legacy AST emitter.
func TestSelfHostParityCorpusX86_64IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "parity_driver")
	for _, tc := range parityCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			probe := runParityDriver(t, runner, driver, tc.src, "-ir-probe")
			if !strings.HasSuffix(strings.TrimRight(probe, "\n"), "module: IR") {
				t.Fatalf("fixture does not route IR (corpus contract):\n%s", probe)
			}
			asm := runParityDriver(t, runner, driver, tc.src, "-ir")
			bin := buildBin(t, gcc, dir, tc.name+"_x86", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			gotOut, gotExit := runBin(cmd, "")
			if gotExit != tc.wantExit {
				t.Errorf("exit = %d, want %d (native interp oracle)", gotExit, tc.wantExit)
			}
			if gotOut != tc.wantOut {
				t.Errorf("stdout = %q, want %q (native interp oracle)", gotOut, tc.wantOut)
			}
		})
	}
}

// TestSelfHostParityCorpusArm64IR is the arm64 leg: asm_ir_run with
// `-target arm64-linux` (built with the Go x86-64 backend, running on the host)
// emits each fixture via the IR path; the aarch64-assembled binary runs
// under qemu and must match the native-interp oracle.
func TestSelfHostParityCorpusArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "parity_driver_arm64")
	for _, tc := range parityCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			asm := runParityDriver(t, x86runner, driver, tc.src, "-target", "arm64-linux", "-ir")
			bin := buildBinArm64(t, arm64gcc, dir, tc.name+"_arm64", asm)
			gotOut, gotExit := runBin(runArm64Bin(qemu, bin), "")
			if gotExit != tc.wantExit {
				t.Errorf("exit = %d, want %d (native interp oracle)", gotExit, tc.wantExit)
			}
			if gotOut != tc.wantOut {
				t.Errorf("stdout = %q, want %q (native interp oracle)", gotOut, tc.wantOut)
			}
		})
	}
}

// TestSelfHostParityCorpusWasmIR is the wasm leg: wasm_ir_run emits each
// fixture via the IR path; the wat runs under wasmtime and must match the
// native-interp oracle.
func TestSelfHostParityCorpusWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm parity corpus e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "parity_driver_wasm")
	for _, tc := range parityCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			wat := runParityDriver(t, runner, driver, tc.src, "-ir")
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, []byte(wat), 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			gotOut, gotExit := runBin(exec.Command("wasmtime", "run", watFile), "")
			if gotExit != tc.wantExit {
				t.Errorf("exit = %d, want %d (native interp oracle)", gotExit, tc.wantExit)
			}
			if gotOut != tc.wantOut {
				t.Errorf("stdout = %q, want %q (native interp oracle)", gotOut, tc.wantOut)
			}
		})
	}
}
