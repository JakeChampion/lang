package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// largeSingleFnProgram builds a single `main` with `n` sequential accumulator
// statements followed by an `acc == acc` guard that always returns 42. Every
// statement lowers to a run of IR ops, so the function's op count scales with
// `n` — the exact shape that exposed the #4652 quadratic: irlower's wasm emit
// path used to re-lower every function once PER collect / gate pass (~30×), and
// each re-lowering paid an O(statements²) ops-array clone into the no-free bump
// arena. Peak RSS grew ≈ 0.16·n² MB (measured: n=40 → 406 MB, n=80 → 1.18 GB,
// n=120 → 2.3 GB), so a large single-function module blew the 3.875 GiB arena and
// the self-host wasm compile was killed (exit 137) — which is what pinned the
// x86_encode/x86_gas migration to the AST path on wasm.
//
// The guard reads `acc` (so the accumulator chain stays live against any DCE)
// but the result is constant, so the wasm exit code is a fixed 42 regardless of
// the arithmetic — the assertion doesn't have to mirror the computation.
func largeSingleFnProgram(n int) string {
	var b strings.Builder
	b.WriteString("function main(): i32 {\n    var acc: i32 = 0;\n")
	for i := 0; i < n; i++ {
		s := strconv.Itoa(i)
		b.WriteString("    acc = acc + " + s + " * 3 - " + s + " + (acc / 2) + (" + s + " % 7);\n")
	}
	b.WriteString("    if (acc == acc) { return 42; }\n    return 1;\n}\n")
	return b.String()
}

// TestSelfHostLargeFnWasmIR compiles a large single-function module through the
// self-host wasm IR path and asserts it (a) emits non-empty WAT and (b) runs to
// exit 42. The point is the SIZE: at 240 statements the pre-#4652 quadratic
// re-lowering needed ≈ 9 GB of bump arena, well past the 3.875 GiB cap, so the
// driver was OOM-killed and produced no WAT. With the lower-once cache
// (wasm_ir.lower_all_for, threaded through the gate + every collect pass) the
// same compile is linear (≈ 0.3 GB), so this is a regression guard: reintroduce
// the per-pass re-lowering and the driver OOMs (empty output / signal: killed)
// instead of emitting a valid module.
func TestSelfHostLargeFnWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host large-fn wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	// 240 statements: comfortably past the ~150-statement point where the old
	// O(passes)·O(statements²) re-lowering exceeded the 3.875 GiB arena.
	src := []byte(largeSingleFnProgram(240))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader(src)
	wat, err := cmd.Output()
	if err != nil {
		t.Fatalf("driver failed to compile large single-function module (a re-introduced "+
			"per-pass re-lowering would OOM here): %v", err)
	}
	if len(wat) == 0 {
		t.Fatal("self-host wasm IR driver emitted 0 bytes for the large single-function module")
	}
	watFile := filepath.Join(dir, "large_fn_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat[:min(len(wat), 400)])
	}
	if code := run.ProcessState.ExitCode(); code != 42 {
		t.Errorf("large-fn wasm IR exit = %d, want 42", code)
	}
}
