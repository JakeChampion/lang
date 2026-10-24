package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const statSizeI64Big = 5 << 30
const statSizeI64BigStr = "5368709120"
const statSizeI64LowStr = "1073741824"

func makeSparse(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create sparse file: %v", err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Skipf("filesystem cannot back a %d-byte sparse file: %v", size, err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() != size {
		t.Skipf("sparse file did not reach %d bytes (got %v, err %v)", size, fi, err)
	}
}

func TestSelfHostStatSizeI64X86(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("stat size test runs only natively (stats a host path)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	bigPath := filepath.Join(dir, "huge.bin")
	makeSparse(t, bigPath, statSizeI64Big)
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	src := fmt.Sprintf(`function main(): i32 {
    match (stat(%q)) {
        Ok(s) => {
            if (s.size == %s) { return 0; }
            if (s.size == %s) { return 42; }
            return 1;
        },
        Err(_) => { return 2; },
    }
}`, bigPath, statSizeI64BigStr, statSizeI64LowStr)

	cmd := exec.Command(driverBin)
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	progBin := buildBin(t, gcc, dir, "stat_size_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("stat size x86 IR exited %d, want 0 (42 = 32-bit truncation, 1 = other)", code)
	}
}

func TestSelfHostStatSizeI64Wasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host stat-size wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	makeSparse(t, filepath.Join(dir, "huge.bin"), statSizeI64Big)
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	src := fmt.Sprintf(`function main(): i32 {
    match (stat("huge.bin")) {
        Ok(s) => {
            if (s.size == %s) { return 0; }
            if (s.size == %s) { return 42; }
            return 1;
        },
        Err(_) => { return 2; },
    }
}`, statSizeI64BigStr, statSizeI64LowStr)

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("i64.load")) {
		t.Fatal("stat-size WAT has no i64.load (size read did not widen to 64-bit)")
	}
	watFile := filepath.Join(dir, "stat_size_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
	run.Dir = dir
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", firstLines(string(wat), 40))
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("stat size wasm IR exited %d, want 0 (42 = 32-bit truncation, 1 = other)", code)
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
