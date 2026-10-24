package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type splitAppendDedupCase struct {
	name string
	src  string
	exit int
}

var splitAppendDedupCases = []splitAppendDedupCase{
	{"split-owned-append", `function main(): i32 {
    let s: string = "a,b,c";
    let parts: string[] = s.split(",");
    let xs: i32[] = [];
    xs = xs.append(1);
    xs = xs.append(2);
    return parts.len() * 10 + xs.len();
}`, 32},
	{"split-plain-append", `function main(): i32 {
    let s: string = "a,b,c,d";
    let parts: string[] = s.split(",");
    let xs: i32[] = [];
    let ys: i32[] = xs.append(7);
    return parts.len() * 10 + ys.len();
}`, 41},
	{"lines-append", `function main(): i32 {
    let s: string = "x\ny\nz";
    let ls: string[] = s.lines();
    let xs: i32[] = [];
    xs = xs.append(1);
    return ls.len() * 10 + xs.len();
}`, 31},
}

func TestSelfHostSplitAppendDedupIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host split/append dedup wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "split_dedup_driver")

	for _, tc := range splitAppendDedupCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			if n := strings.Count(string(wat), "(func $__fern_arr_push "); n != 1 {
				t.Errorf("%s: $__fern_arr_push defined %d times, want 1", tc.name, n)
			}
			watFile := filepath.Join(dir, "split_dedup_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("split/append dedup wasm IR %q = %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
