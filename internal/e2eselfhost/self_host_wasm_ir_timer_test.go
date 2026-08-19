package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmIRTimerPollable is the first real preview2 op lowered on the
// self-host wasm-IR path (#3457 phase / #4317): the wasm reactor's timer
// primitives wasm_timer_pollable + wasm_pollable_drop.
//
// wasm_timer_pollable(dur_ns: i64) -> pollable subscribes a monotonic-clock
// timer (wasi:clocks/monotonic-clock.subscribe-duration) and returns the
// wasi:io/poll pollable handle; wasm_pollable_drop(p) drops it
// (wasi:io/poll.[resource-drop]pollable). wasm_ir emits these as DIRECT
// component-model imports, so the emitted core module must be composed with
// `wasm-tools component new` before it runs (a bare core module can't resolve a
// preview2 interface) — building on the component-adapter foundation (#4315).
//
// The test drives the IR path, asserts the emitted WAT carries the two preview2
// imports (proving the ops are on the IR path, not the AST fallback), composes
// the core into a component, checks it imports wasi:io/poll +
// wasi:clocks/monotonic-clock, and runs it: the pollable is created and dropped,
// exit 0. (It does not block on the pollable — wasm_block is not yet an IR op —
// so it exercises the create+drop resource lifecycle, not the wait.)
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
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasm_ir_run")

	// A monotonic-clock timer pollable, created then dropped. The duration is a
	// typed i64 local (a direct i64 literal in argument position mis-infers to
	// i32 — a separate pre-existing lowering quirk, unrelated to this op).
	const src = `function main(): i32 {
    var d: i64 = 200000000i64;
    var p: i32 = wasm_timer_pollable(d);
    wasm_pollable_drop(p);
    return 0;
}`
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm_ir_run -ir failed: %v", err)
	}

	// The emitted WAT must carry the two preview2 imports — proof the ops lowered
	// on the IR path (the AST fallback emits neither).
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

	// The component must import the two preview2 interfaces the ops resolve to.
	wit, _ := exec.Command(wasmtools, "component", "wit", compPath).Output()
	for _, want := range []string{"wasi:io/poll@0.2", "wasi:clocks/monotonic-clock@0.2"} {
		if !strings.Contains(string(wit), want) {
			t.Errorf("component missing import %s", want)
		}
	}

	// Runs: create + drop the pollable, exit 0.
	runCmd := exec.Command(wasmtime, "run", compPath)
	if out, err := runCmd.CombinedOutput(); err != nil {
		t.Fatalf("wasmtime run timer component: %v\n%s", err, out)
	}
	if code := runCmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("timer component exit = %d, want 0", code)
	}
}
