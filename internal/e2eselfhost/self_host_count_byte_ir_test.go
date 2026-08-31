package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__count_byte(s, byte)` on the self-host IR path — the fourth fused kernel of
// docs/ATLAS-PLATFORM-PLAN.md §3, and the first with no early exit.
//
// It lands SCALAR on all seven backends at once, which is §3.4's step 1: the
// intrinsic becomes total before anything depends on it being fast, so no step
// can leave the tree unbuildable.
//
// What a port gets wrong here is not the clamp — there is no cursor, so there
// is nothing to clamp. It is the ACCUMULATOR: a body that returns on the first
// match, or loses the running total across a block boundary, answers every
// single-occurrence case correctly and fails only where matches are dense. The
// sweep below therefore checks the all-match and complement cases at every
// length, not just the one-needle-at-each-position shape its siblings use.

// countByteIRProg is SELF-CHECKING: it carries its own reference
// implementation in Fern and compares `__count_byte` against it, so the corpus
// sweeps exhaustively with no Go-side expectation list to keep in step.
//
// Lengths 0..40 — two full 16-byte blocks plus a partial tail either side, so
// the boundaries a vector body will later have are already covered.
//
// A failure returns a small distinct code rather than a count, so the exit
// status says WHICH shape disagreed. 42 means every comparison matched.
const countByteIRProg = `function ref(s: string, b: i32): i32 {
    if (b < 0) { return 0; }
    if (b > 255) { return 0; }
    var i: i32 = 0;
    var c: i32 = 0;
    while (i < s.len()) {
        if ((s[i] as i32) == b) { c = c + 1; }
        i = i + 1;
    }
    return c;
}
function main(): i32 {
    var n: i32 = 0;
    while (n <= 40) {
        var base: string = "";
        var k: i32 = 0;
        while (k < n) { base = base + "a"; k = k + 1; }
        if (__count_byte(base, 122) != ref(base, 122)) { return 1; }
        if (__count_byte(base, 97) != ref(base, 97)) { return 2; }
        var at: i32 = 0;
        while (at < n) {
            var s: string = slice_unchecked(base, 0, at) + "z" + slice_unchecked(base, at + 1, n);
            if (__count_byte(s, 122) != ref(s, 122)) { return 3; }
            if (__count_byte(s, 97) != ref(s, 97)) { return 4; }
            at = at + 1;
        }
        n = n + 1;
    }
    var alt: string = "";
    var j: i32 = 0;
    while (j < 100) { alt = alt + "ab"; j = j + 1; }
    if (__count_byte(alt, 97) != 100) { return 5; }
    if (__count_byte(alt, 98) != 100) { return 6; }
    if (__count_byte("", 97) != 0) { return 7; }
    if (__count_byte("abc", 256) != 0) { return 8; }
    if (__count_byte("abc", 0 - 1) != 0) { return 9; }
    if (__count_byte("aaa", 256) != 0) { return 10; }
    if (__count_byte("abcabc", 98) != 2) { return 11; }
    return 42;
}
`

// runCountByteIR compiles countByteIRProg with the self-host modload driver for
// the given register target and returns the exit code.
func runCountByteIR(t *testing.T, target string) int {
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

	progAsm, progDir := compileSourceModload(t, runner, driverBin, countByteIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "count_byte_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostCountByteIRX86_64(t *testing.T) {
	if got := runCountByteIR(t, "x86-64-linux"); got != 42 {
		t.Errorf("__count_byte self-host x86-64 = %d, want 42 (see countByteIRProg for what each code means)", got)
	}
}

func TestSelfHostCountByteIRArm64(t *testing.T) {
	if got := runCountByteIR(t, "arm64-linux"); got != 42 {
		t.Errorf("__count_byte self-host arm64 = %d, want 42 (see countByteIRProg for what each code means)", got)
	}
}

// TestSelfHostCountByteIRWasm runs the same program through the self-hosted
// wasm IR driver. The helper's presence in the emitted text is asserted, so a
// module that silently stopped needing it would fail rather than pass by
// exercising nothing.
func TestSelfHostCountByteIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host count_byte wasm IR e2e")
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
	cmd.Stdin = strings.NewReader(countByteIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_count_byte")) {
		t.Fatal("emitted wat has no $__fern_count_byte helper — the op did not lower")
	}
	watFile := filepath.Join(dir, "count_byte.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 42 {
		t.Errorf("__count_byte self-host wasm = %d, want 42 (see countByteIRProg for what each code means)", code)
	}
}
