package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostJoinWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host join wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"multi", `function main(): i32 { let xs: string[] = []; xs = xs.append("a"); xs = xs.append("bb"); xs = xs.append("ccc"); return xs.join("-").len(); }`, 8},
		{"empty", `function main(): i32 { let xs: string[] = []; return xs.join(",").len(); }`, 0},
		{"single", `function main(): i32 { let xs: string[] = []; xs = xs.append("solo"); return xs.join(",").len(); }`, 4},
		{"empty-sep", `function main(): i32 { let xs: string[] = []; xs = xs.append("a"); xs = xs.append("b"); xs = xs.append("c"); return xs.join("").len(); }`, 3},
		{"multi-char-sep", `function main(): i32 { let xs: string[] = []; xs = xs.append("x"); xs = xs.append("y"); xs = xs.append("z"); return xs.join(" - ").len(); }`, 9},
		{"concat-result", `function main(): i32 { let xs: string[] = []; xs = xs.append("a"); xs = xs.append("b"); let s: string = "[" + xs.join(",") + "]"; return s.len(); }`, 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !strings.Contains(string(wat), "$__fern_str_join") {
				t.Errorf("%q did not reach the IR join helper (no $__fern_str_join in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, "join_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("join wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
