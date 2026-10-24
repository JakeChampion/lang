package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const nullStructDropProg = `struct P { n: i32, xs: i32[] }
@noinline function inner_size(p: P): i32 { return p.xs.len(); }
function main(): i32 {
    let lp: P = P { n: 1, xs: [1, 2] };
    let s: i32 = inner_size(lp);
    let r: i32 = random_i32();
    while ((r & 1) != 0 || r < 16777216) { r = random_i32(); }
    return s;
}
`

func TestSelfHostNullStructDropWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping null-struct-drop wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(nullStructDropProg))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "null_struct_drop.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	rcmd := exec.Command("wasmtime", "run", watFile)
	rcmd.Stdin = bytes.NewReader(nil)
	_ = rcmd.Run()
	if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally")
	}
	if got := rcmd.ProcessState.ExitCode(); got != 2 {
		t.Errorf("program = %d, want 2 (134 = the null box walked the low scratch)", got)
	}
}

func wasmFuncBodies(wat, prefix string) []string {
	var out []string
	lines := strings.Split(wat, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "(func "+prefix) {
			continue
		}
		body := []string{line}
		for j := i + 1; j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "(func "); j++ {
			body = append(body, lines[j])
		}
		out = append(out, strings.Join(body, "\n"))
	}
	return out
}
