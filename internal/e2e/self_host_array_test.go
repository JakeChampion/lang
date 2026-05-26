package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// arrayMain exercises std/array's gcd_all (which needs i32.gcd) on a
// bundle of array + sort (sort provides the sort_* funcs array refs).
const arrayMain = "import \"./array\";\n" +
	"function main(): i32 { var xs: i32[] = [12, 18, 24]; match (array.__method_Array_gcd_all(xs)) { Some(g) => { return g; }, None => { return 0; } } return 0; }\n"

func arrayBundle(t *testing.T) []byte {
	t.Helper()
	a, err := os.ReadFile("../../internal/stdlib/std/array.fern")
	if err != nil {
		t.Fatalf("read array: %v", err)
	}
	srt, err := os.ReadFile("../../internal/stdlib/std/sort.fern")
	if err != nil {
		t.Fatalf("read sort: %v", err)
	}
	var b []byte
	b = append(b, "///MODULE array\n"...)
	b = append(b, a...)
	b = append(b, "\n///MODULE sort\n"...)
	b = append(b, srt...)
	b = append(b, "\n///MODULE main\n"...)
	b = append(b, arrayMain...)
	return b
}

// TestSelfHostArrayX86_64 — the self-hosted compiler compiles real
// std/array (needed i32.gcd/lcm); gcd_all([12,18,24]) == 6.
func TestSelfHostArrayX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "bundle_run.fern"} {
		src, _ := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "bundle_run.fern", "driver")
	asm := runCapture(t, gcc, runner, driverBin, arrayBundle(t))
	progBin := buildBin(t, gcc, dir, "arrprog", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 6 {
		t.Errorf("gcd_all([12,18,24]) exited %d, want 6", code)
	}
}

// TestSelfHostArrayArm64 — CI-gated arm64 counterpart.
func TestSelfHostArrayArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm_arm64.fern", "flatten.fern", "bundle_run_arm64.fern"} {
		src, _ := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "bundle_run_arm64.fern", "driver")
	asm := runCapture(t, x86gcc, x86runner, driverBin, arrayBundle(t))
	progBin := buildBin(t, arm64gcc, dir, "arrprog", string(asm))
	cmd := runArm64Bin(qemu, progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 6 {
		t.Errorf("gcd_all([12,18,24]) exited %d, want 6", code)
	}
}
