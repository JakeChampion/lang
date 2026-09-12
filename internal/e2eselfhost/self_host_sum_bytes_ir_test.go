package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__sum_bytes(s)` on the self-host IR path — the sixth fused kernel of
// docs/ATLAS-PLATFORM-PLAN.md §3, and the family's first true reduction.
//
// This is the leg the Go-side suite cannot stand in for. `internal/e2e`'s
// differential proves the NATIVE emitters; the self-host op, its two native
// assembly bodies and its wat helper are a separate implementation that no
// Go test reaches. An incomplete mirror there passes every Go test in the
// tree, which is how #9060's sleep_ns and the two overnight defects reached
// main.
//
// What a port of a reduction gets wrong is not a clamp — there is no cursor.
// It is the ACCUMULATOR lost across a block boundary, and SIGN EXTENSION: a
// body that loads bytes with a sign-extending move agrees on every ASCII
// case and fails every case above 0x7f. Half the sweep below is therefore
// high-byte, which is what separates those two bugs from a working body.

// sumBytesIRProg is SELF-CHECKING: it carries its own reference in Fern and
// compares `__sum_bytes` against it, so the sweep is exhaustive with no
// Go-side expectation list to keep in step.
//
// Lengths 0..40 — two full 16-byte blocks plus a partial tail either side, so
// a vector body's boundaries are covered before one exists.
//
// A failure returns a small distinct code rather than a sum, so the exit
// status says WHICH shape disagreed. 42 means every comparison matched.
const sumBytesIRProg = `function ref(s: string): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < s.len()) {
        acc = acc + (s[i] as i32);
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var n: i32 = 0;
    while (n <= 40) {
        var base: string = "";
        var k: i32 = 0;
        while (k < n) { base = base + "a"; k = k + 1; }
        if (__sum_bytes(base) != ref(base)) { return 1; }
        if (__sum_bytes(base) != n * 97) { return 2; }
        var at: i32 = 0;
        while (at < n) {
            var hi: string = slice_unchecked(base, 0, at) + "\xff" + slice_unchecked(base, at + 1, n);
            if (__sum_bytes(hi) != ref(hi)) { return 3; }
            if (__sum_bytes(hi) != (n - 1) * 97 + 255) { return 4; }
            var z: string = slice_unchecked(base, 0, at) + "\x00" + slice_unchecked(base, at + 1, n);
            if (__sum_bytes(z) != ref(z)) { return 5; }
            at = at + 1;
        }
        n = n + 1;
    }
    var m: i32 = 0;
    while (m <= 40) {
        var all: string = "";
        var k2: i32 = 0;
        while (k2 < m) { all = all + "\xff"; k2 = k2 + 1; }
        if (__sum_bytes(all) != ref(all)) { return 6; }
        if (__sum_bytes(all) != m * 255) { return 7; }
        m = m + 1;
    }
    if (__sum_bytes("") != 0) { return 8; }
    if (__sum_bytes("abc") != 294) { return 9; }
    if (__sum_bytes("\x00b\x00") != 98) { return 10; }
    if (__sum_bytes("\x7f\x80") != 255) { return 11; }
    if (__sum_bytes("\x80\x80\x80\x80") != 512) { return 12; }
    return 42;
}
`

// runSumBytesIR compiles sumBytesIRProg with the self-host modload driver for
// the given register target and returns the exit code.
func runSumBytesIR(t *testing.T, target string) int {
	t.Helper()
	var runner, runPrefix, extra []string
	var driverBin, linkGcc string
	if target == "arm64-linux" {
		var qemu string
		_, runner, driverBin = buildModloadArm64DriverX86(t)
		linkGcc, qemu = arm64Tooling(t)
		if qemu != "" {
			runPrefix = []string{qemu}
		}
		extra = []string{"-target", "arm64-linux"}
	} else {
		linkGcc, runner, driverBin = buildModloadDriverX86(t)
		runPrefix = runner
	}

	progAsm, progDir := compileSourceModload(t, runner, driverBin, sumBytesIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "sum_bytes_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostSumBytesIRX86_64(t *testing.T) {
	if got := runSumBytesIR(t, "x86-64-linux"); got != 42 {
		t.Errorf("__sum_bytes self-host x86-64 = %d, want 42 (see sumBytesIRProg for what each code means)", got)
	}
}

func TestSelfHostSumBytesIRArm64(t *testing.T) {
	if got := runSumBytesIR(t, "arm64-linux"); got != 42 {
		t.Errorf("__sum_bytes self-host arm64 = %d, want 42 (see sumBytesIRProg for what each code means)", got)
	}
}

// TestSelfHostSumBytesIRWasm runs the same program through the self-hosted
// wasm IR driver. The helper's presence in the emitted text is asserted, so a
// module that silently stopped needing it would fail rather than pass by
// exercising nothing.
func TestSelfHostSumBytesIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host sum_bytes wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = strings.NewReader(sumBytesIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_sum_bytes")) {
		t.Fatal("emitted wat has no $__fern_sum_bytes helper — the op did not lower")
	}
	watFile := filepath.Join(dir, "sum_bytes.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 42 {
		t.Errorf("__sum_bytes self-host wasm = %d, want 42 (see sumBytesIRProg for what each code means)", code)
	}
}
