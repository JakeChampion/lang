package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostIRRoundTrip exercises the self-hosted stack IR
// (examples/self_host/ir.fern, slice 0 of the IR rebuild — see
// docs/RC-PERCEUS-SELF-HOST-IR-REBUILD.md). The ir_run driver builds a
// representative Op[] via the constructor helpers — one per opcode family,
// including the Perceus-relevant `call_direct __fern_rc_inc` — renders each
// with render_op, and prints the stream one op per line. This pins the IR's
// data shape + constructors + printer end-to-end through the self-host ->
// native pipeline, proving ir.fern compiles (not just type-checks) and that
// every field an opcode uses round-trips through render_op.
//
// The driver is built natively via the Go x86-64 backend; its stdout is the
// rendered stream and its exit code is ops.len().
func TestSelfHostIRRoundTrip(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ir_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "ir.fern", "ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, gcc, dir, "ir_run.fern", "ir_run")

	// Golden render of the Op[] the driver builds — locks the printer and
	// every constructor's field placement (the width_ptr() sentinel renders
	// as w-1; call_direct carries callee/argc; br/brif carry their depth).
	const want = "const_i32 42\n" +
		"const_str hi\n" +
		"load_local 3\n" +
		"store_local 3\n" +
		"tee_local 0\n" +
		"add\n" +
		"div_s\n" +
		"load w-1\n" +
		"store w-1\n" +
		"alloc\n" +
		"call_direct __fern_rc_inc/1\n" +
		"call_indirect\n" +
		"call_closure_direct\n" +
		"block\n" +
		"loop\n" +
		"if\n" +
		"else\n" +
		"br 2\n" +
		"brif 1\n" +
		"end\n" +
		"drop\n" +
		"return\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("ir_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("render_op stream mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// The driver returns ops.len() (22) as its exit code — a second,
	// independent check that the Op[] built and iterated to completion.
	if code := cmd.ProcessState.ExitCode(); code != 22 {
		t.Errorf("ir_run exit code = %d, want 22 (ops.len())", code)
	}
}
