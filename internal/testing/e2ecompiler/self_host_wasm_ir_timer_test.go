package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostWasmIRTimerPollable(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR timer e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-IR timer e2e")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset; skipping wasm-IR timer e2e")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %s not found; skipping", adapter)
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "wasm_ir_run")

	const src = `function main(): i32 {
    let d: i64 = 200000000i64;
    let p: i32 = wasm_timer_pollable(d);
    wasm_pollable_drop(p);
    return 0;
}`
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm_ir_run failed: %v", err)
	}

	for _, want := range []string{
		`"wasi:clocks/monotonic-clock@0.2.0" "subscribe-duration"`,
		`"wasi:io/poll@0.2.0" "[resource-drop]pollable"`,
	} {
		if !strings.Contains(string(wat), want) {
			t.Fatalf("emitted WAT missing preview2 import %s\n%s", want, wat)
		}
	}

	watPath := filepath.Join(dir, "timer.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "timer.core.wasm")
	if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse: %v\n%s", err, out)
	}
	compPath := filepath.Join(dir, "timer.component.wasm")
	if out, err := exec.Command(wasmtools, "component", "new", corePath,
		"--adapt", "wasi_snapshot_preview1="+adapter, "-o", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools component new --adapt: %v\n%s", err, out)
	}
	if out, err := exec.Command(wasmtools, "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}

	wit, _ := exec.Command(wasmtools, "component", "wit", compPath).Output()
	for _, want := range []string{"wasi:io/poll@0.2", "wasi:clocks/monotonic-clock@0.2"} {
		if !strings.Contains(string(wit), want) {
			t.Errorf("component missing import %s", want)
		}
	}

	runCmd := exec.Command(wasmtime, "run", compPath)
	if out, err := runCmd.CombinedOutput(); err != nil {
		t.Fatalf("wasmtime run timer component: %v\n%s", err, out)
	}
	if code := runCmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("timer component exit = %d, want 0", code)
	}
}
