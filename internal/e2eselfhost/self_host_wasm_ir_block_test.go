package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmIRBlock covers wasm_block on the self-host wasm-IR path (#3457
// phase / #4317 follow-up): the primitive that makes a wasm_timer_pollable timer
// observably block.
//
// wasm_block(p: i32) -> i32 synchronously waits until the pollable p is ready
// (wasi:io/poll.[method]pollable.block), then returns 0. Combined with
// wasm_timer_pollable (subscribe a monotonic-clock timer) it is a real sleep:
// subscribe -> block -> the duration elapses before control returns. wasm_ir
// emits it as a DIRECT component-model import, so the emitted core module must be
// composed with `wasm-tools component new` before it runs (a bare core module
// can't resolve a preview2 interface) — building on the timer-pollable foundation.
//
// The test drives the IR path, asserts the emitted WAT carries the
// [method]pollable.block preview2 import (proving the op is on the IR path, not
// the AST fallback), composes the core into a component, checks it imports
// wasi:io/poll + wasi:clocks/monotonic-clock, and runs it: subscribe a short
// timer, block on it, drop it, exit 0.
func TestSelfHostWasmIRBlock(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR block e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-IR block e2e")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset; skipping wasm-IR block e2e")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %s not found; skipping", adapter)
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasm_ir_run")

	// Subscribe a short monotonic-clock timer, block on the pollable until it
	// fires, then drop it. The duration is a typed i64 local (a direct i64 literal
	// in argument position mis-infers to i32 — a separate pre-existing lowering
	// quirk, unrelated to this op).
	const src = `function main(): i32 {
    var d: i64 = 20000000i64;
    var p: i32 = wasm_timer_pollable(d);
    var r: i32 = wasm_block(p);
    wasm_pollable_drop(p);
    return r;
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

	// The emitted WAT must carry the three preview2 imports — proof the ops
	// lowered on the IR path (the AST fallback emits none).
	for _, want := range []string{
		`"wasi:clocks/monotonic-clock@0.2.0" "subscribe-duration"`,
		`"wasi:io/poll@0.2.0" "[method]pollable.block"`,
		`"wasi:io/poll@0.2.0" "[resource-drop]pollable"`,
	} {
		if !strings.Contains(string(wat), want) {
			t.Fatalf("emitted WAT missing preview2 import %s\n%s", want, wat)
		}
	}

	watPath := filepath.Join(dir, "block.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "block.core.wasm")
	if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse: %v\n%s", err, out)
	}
	compPath := filepath.Join(dir, "block.component.wasm")
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

	// Runs: subscribe + block + drop the pollable, exit 0.
	runCmd := exec.Command(wasmtime, "run", compPath)
	if out, err := runCmd.CombinedOutput(); err != nil {
		t.Fatalf("wasmtime run block component: %v\n%s", err, out)
	}
	if code := runCmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("block component exit = %d, want 0", code)
	}
}
