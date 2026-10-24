package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostMethodReturnsClosureIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host method-returns-closure IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
	}{
		{
			name: "noncapturing",
			src: `struct Maker { }
function (m: Maker) make(): (i32) => i32 { return (x: i32): i32 => { return x * 2; }; }
function main(): i32 { let m = Maker { }; let f = m.make(); return f(21); }`,
		},
		{
			name: "captures-method-param",
			src: `struct F { }
function (f: F) mul(k: i32): (i32) => i32 { return (x: i32): i32 => { return x * k; }; }
function main(): i32 { let f = F { }; let g = f.mul(7); return g(6); }`,
		},
		{
			name: "called-without-binding",
			src: `struct Maker { }
function (m: Maker) make(): (i32) => i32 { return (x: i32): i32 => { return x * 3; }; }
function main(): i32 { let m = Maker { }; return m.make()(14); }`,
		},
		{
			name: "free-fn-unaffected",
			src: `function make(): (i32) => i32 { return (x: i32): i32 => { return x * 2; }; }
function main(): i32 { let f = make(); return f(21); }`,
		},
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
				t.Fatalf("driver failed: %v", err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally:\n%s", wat)
			}
			want := interpExit(t, interpBin, tc.src)
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s: wasm exited %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
