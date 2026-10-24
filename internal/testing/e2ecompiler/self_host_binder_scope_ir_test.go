package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var binderScopeCases = []struct {
	name string
	src  string
}{
	{"at-binding-captured-by-a-lambda", `enum Box { Full(i32), Empty }

function total(b: Box): i32 { match (b) { Full(v) => { return v; }, Empty => { return 0; } } return 0; }

function f(b: Box): i32 {
  match (b) {
    n @ Full(v) => {
      let g: (i32) => i32 = (d: i32): i32 => { return total(n) + d; };
      return g(1);
    },
    Empty => { return 0; },
  }
  return 0;
}

function main(): i32 { return f(Full(5)); }`},
	{"tuple-destructured-fn-values-are-bound", `function inc(n: i32): i32 { return n + 1; }
function dbl(n: i32): i32 { return n * 2; }
function pair(): ((i32) => i32, (i32) => i32) { return (inc, dbl); }
function main(): i32 {
  let (g, h) = pair();
  return g(1) + h(2);
}`},
}

func TestSelfHostBinderScopeIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range binderScopeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s exited %d, native interp says %d", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostBinderScopeWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host binder-scope wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range binderScopeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("self-host wasm compiler emitted 0 bytes")
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watFile)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s exited %d, native interp says %d", tc.name, got, want)
			}
		})
	}
}
