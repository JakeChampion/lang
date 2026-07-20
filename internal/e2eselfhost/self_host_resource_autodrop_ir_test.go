package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// wasmIRResourceDriver builds the wasm_ir_run driver (the wasm IR-path
// differential driver) in a temp dir and returns (dir, driverBin, gcc, runner).
// Shared by the resource auto-drop IR tests below.
func wasmIRResourceDriver(t *testing.T) (string, string, string, []string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir, buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasm_ir_run"), gcc, runner
}

// watFuncBody extracts the body text of `(func $name ...)` from a WAT dump:
// everything from its header line to the next top-level `  (func ` (or the
// module end). Coarse but sufficient for per-function containment asserts.
func watFuncBody(t *testing.T, wat, name string) string {
	t.Helper()
	marker := "  (func $" + name
	i := strings.Index(wat, marker)
	if i < 0 {
		t.Fatalf("WAT has no function %s\n%s", name, wat)
	}
	rest := wat[i+len(marker):]
	if j := strings.Index(rest, "\n  (func "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// TestSelfHostResourceAutoDropIRWasm is the self-host IR-path parity gate for
// P5 slice 3 (#5337, docs/WIT-BRING-YOUR-OWN.md): AUTOMATIC `own R` drop on
// the wasm IR path. The program declares NO drop function; previously the IR
// funnel (irlower.lift_lambdas) never inserted the drop defers — the insertion
// lived only in the AST prep (module_with_builtins → lower_defers_prepass) —
// and the IR emitters turned every `@import` extern into a silent no-op stub,
// so owned resources were never finalized on the IR path. Now
// insert_resource_drops_module runs in the IR funnel too, the externs (and the
// synthesized `[resource-drop]pollable`) are emitted as real core imports, and
// the composed component runs under real WASI with the pollable released.
//
// Same compose harness as TestSelfHostWasmIRTimerPollable: the emitted
// preview1+preview2 core is componentized with `wasm-tools component new
// --adapt` and run under wasmtime.
func TestSelfHostResourceAutoDropIRWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping resource auto-drop IR e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping resource auto-drop IR e2e")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset; skipping resource auto-drop IR e2e")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %s not found; skipping", adapter)
	}
	dir, driverBin, gcc, runner := wasmIRResourceDriver(t)

	// No drop function declared — the compiler must insert it. The borrowed
	// handle lent to block()/ready() must NOT be dropped there; the owned
	// local p is dropped on the exit path.
	const want = "poll-ok"
	prog := `@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: borrow Pollable);

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;

function main(): i32 {
    var p: own Pollable = subscribe(0 as u64);
    block(p);
    if (ready(p)) { write("` + want + `"); } else { write("poll-bad"); }
    return 0;
}`
	wat := string(runCapture(t, gcc, runner, driverBin, []byte(prog), "-ir"))
	if len(wat) == 0 {
		t.Fatal("wasm_ir_run -ir produced 0 bytes")
	}
	// The synthesized resource-drop must be a real core IMPORT (the program
	// never names it), and the extern declarations must be imports too — not
	// the no-op stub definitions the IR path used to emit.
	for _, wantFrag := range []string{
		`(import "wasi:io/poll@0.2.0" "[resource-drop]pollable" (func $__resource_drop_Pollable (param i32)))`,
		`(import "wasi:clocks/monotonic-clock@0.2.0" "subscribe-duration" (func $subscribe (param i64) (result i32)))`,
		`(import "wasi:io/poll@0.2.0" "[method]pollable.block" (func $block (param i32)))`,
		`(import "wasi:io/poll@0.2.0" "[method]pollable.ready" (func $ready (param i32) (result i32)))`,
		"call $__resource_drop_Pollable",
	} {
		if !strings.Contains(wat, wantFrag) {
			t.Fatalf("emitted WAT missing %s\n%s", wantFrag, wat)
		}
	}
	for _, stub := range []string{
		"\n  (func $__resource_drop_Pollable",
		"\n  (func $subscribe",
		"\n  (func $block",
		"\n  (func $ready",
	} {
		if strings.Contains(wat, stub) {
			t.Fatalf("emitted WAT still defines extern stub %q (should be import-only)\n%s", stub, wat)
		}
	}

	watPath := filepath.Join(dir, "autodrop.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "autodrop.core.wasm")
	if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse: %v\n%s", err, out)
	}
	compPath := filepath.Join(dir, "autodrop.component.wasm")
	if out, err := exec.Command(wasmtools, "component", "new", corePath,
		"--adapt", "wasi_snapshot_preview1="+adapter, "-o", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools component new --adapt: %v\n%s", err, out)
	}
	if out, err := exec.Command(wasmtools, "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	runCmd := exec.Command(wasmtime, "run", compPath)
	out, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run auto-drop component: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), want) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
	if code := runCmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("auto-drop component exit = %d, want 0", code)
	}
}

// TestSelfHostResourceAutoDropIRWasmMoved pins the sound-over-complete side of
// the IR-path auto-drop (#5337, mirroring the native movedHandles contract):
// an owned handle that ESCAPES — returned from its function, or passed to an
// `own` parameter — is treated as moved and NOT dropped by its declarer, and
// an owned PARAMETER is never dropped (only locals are). Only the kept owned
// local (`b` in main) gets the inserted drop.
func TestSelfHostResourceAutoDropIRWasmMoved(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping resource auto-drop IR e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping resource auto-drop IR e2e")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset; skipping resource auto-drop IR e2e")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %s not found; skipping", adapter)
	}
	dir, driverBin, gcc, runner := wasmIRResourceDriver(t)

	// make(): q is RETURNED -> moved, no drop in make.
	// consume(h: own Pollable): h is a param -> never dropped.
	// main: a is passed to consume's own param -> moved, no drop for a;
	//       b stays owned -> exactly the local that gets the drop.
	const want = "moved-ok"
	prog := `@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: borrow Pollable);

function make(): own Pollable {
    var q: own Pollable = subscribe(0 as u64);
    return q;
}

function consume(h: own Pollable): i32 {
    block(h);
    return 1;
}

function main(): i32 {
    var a: own Pollable = make();
    var r: i32 = consume(a);
    var b: own Pollable = subscribe(0 as u64);
    block(b);
    if (r == 1) { write("` + want + `"); } else { write("moved-bad"); }
    return 0;
}`
	wat := string(runCapture(t, gcc, runner, driverBin, []byte(prog), "-ir"))
	if len(wat) == 0 {
		t.Fatal("wasm_ir_run -ir produced 0 bytes")
	}
	if !strings.Contains(wat, `(import "wasi:io/poll@0.2.0" "[resource-drop]pollable" (func $__resource_drop_Pollable (param i32)))`) {
		t.Fatalf("emitted WAT missing the synthesized resource-drop import\n%s", wat)
	}
	// make's returned handle and consume's own param must NOT be dropped.
	for _, fn := range []string{"make", "consume"} {
		if body := watFuncBody(t, wat, fn); strings.Contains(body, "__resource_drop_") {
			t.Fatalf("%s must not drop its moved/param handle, but its body calls a drop:\n%s", fn, body)
		}
	}
	// main's kept owned local IS dropped.
	if body := watFuncBody(t, wat, "main"); !strings.Contains(body, "call $__resource_drop_Pollable") {
		t.Fatalf("main's kept owned local is not dropped:\n%s", body)
	}

	watPath := filepath.Join(dir, "moved.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "moved.core.wasm")
	if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse: %v\n%s", err, out)
	}
	compPath := filepath.Join(dir, "moved.component.wasm")
	if out, err := exec.Command(wasmtools, "component", "new", corePath,
		"--adapt", "wasi_snapshot_preview1="+adapter, "-o", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools component new --adapt: %v\n%s", err, out)
	}
	if out, err := exec.Command(wasmtools, "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	runCmd := exec.Command(wasmtime, "run", compPath)
	out, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run moved component: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), want) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
	if code := runCmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("moved component exit = %d, want 0", code)
	}
}
