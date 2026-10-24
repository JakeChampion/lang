package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

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

type parityCase struct {
	name, src, wantOut string
	wantExit           int
}

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

func TestSelfHostParityCorpusX86_64IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "parity_driver")
	for _, tc := range parityCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			probe := runParityDriver(t, runner, driver, tc.src, "-ir-probe")
			if !strings.HasSuffix(strings.TrimRight(probe, "\n"), "module: IR") {
				t.Fatalf("fixture does not route IR (corpus contract):\n%s", probe)
			}
			asm := runParityDriver(t, runner, driver, tc.src)
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

func TestSelfHostParityCorpusArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "parity_driver_arm64")
	for _, tc := range parityCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			asm := runParityDriver(t, x86runner, driver, tc.src, "-target", "arm64-linux")
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

func TestSelfHostParityCorpusWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm parity corpus e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "parity_driver_wasm")
	for _, tc := range parityCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			wat := runParityDriver(t, runner, driver, tc.src)
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
