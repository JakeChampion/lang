package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var closureLocalOptRetCases = []struct {
	name string
	src  string
	want int
}{
	{"alias", `
function main(): i32 {
    let f: () => Option[i32] = (): Option[i32] => { return Some(7); };
    let g: () => Option[i32] = f;
    match (g()) { Some(v) => { return v; }, None => { return 1; } }
}
`, 7},
	{"fn-param", `
function call(f: () => Option[i32]): i32 {
    match (f()) { Some(v) => { return v; }, None => { return 1; } }
}
function main(): i32 { return call((): Option[i32] => { return Some(6); }); }
`, 6},
	{"named-fn-init", `
function g(): Option[i32] { return Some(5); }
function main(): i32 {
    let f: () => Option[i32] = g;
    match (f()) { Some(v) => { return v; }, None => { return 1; } }
}
`, 5},
	{"direct", `
function main(): i32 {
    let f: () => Option[i32] = (): Option[i32] => { return Some(5); };
    match (f()) { Some(v) => { return v; }, None => { return 1; } }
}
`, 5},
	{"split-call", `
function main(): i32 {
    let f: () => Option[i32] = (): Option[i32] => { return Some(5); };
    let o: Option[i32] = f();
    match (o) { Some(v) => { return v; }, None => { return 1; } }
}
`, 5},
	{"non-option-closure-array", `
function main(): i32 {
    let fs: (() => i32)[] = [(): i32 => { return 9; }];
    return fs[0]();
}
`, 9},
	{"string-closure-local", `
function main(): i32 {
    let f: () => string = (): string => { return "abcde"; };
    return f().len();
}
`, 5},
}

func TestSelfHostClosureLocalOptRetIRX86_64(t *testing.T) {
	gcc, runner, driverBin := strictIRDriver(t)
	dir := filepath.Dir(driverBin)

	for _, tc := range closureLocalOptRetCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, stderr, code := runDriver(t, runner, driverBin, []byte(tc.src), true)
			if code != 0 || len(asm) == 0 {
				t.Fatalf("%s did not lower on the IR path (exit %d):\n%s", tc.name, code, stderr)
			}
			progBin := buildBin(t, gcc, dir, "clooptret_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostClosureLocalOptRetIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host closure-local opt-ret wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range closureLocalOptRetCases {
		t.Run(tc.name, func(t *testing.T) {
			wat, stderr, code := runDriver(t, runner, driverBin, []byte(tc.src), true)
			if code != 0 || len(wat) == 0 {
				t.Fatalf("%s did not lower on the wasm IR path (exit %d):\n%s", tc.name, code, stderr)
			}
			watFile := filepath.Join(dir, "closure_local_optret_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := run.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("closure-local opt-ret wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
