package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__mismatch(a, ao, b, bo, n)` on the self-host IR path — the fifth fused SIMD
// kernel (#8791), and the comparison one the other four left out.
//
// ONE kernel answers both questions a caller can ask: equality is
// `__mismatch(..) == n`, ordering is one indexed load at the returned offset.
// So the same corpus that proves the offset right proves both callers right,
// which is why these tests check the offset itself rather than a boolean.
//
// The three self-host backends run three different bodies — SSE2 on x86-64,
// NEON on arm64, v128 on wasm — and the sub-16 band on the two native tiers
// takes memcmp's overlapping 8- and 4-byte windows rather than the vector loop.
// Holding all of them to one expectation is what these tests are for: a length
// sweep that crosses every one of those boundaries, run identically on each.

// mismatchIRProg is SELF-CHECKING: it carries its own reference implementation
// written in Fern and compares `__mismatch` against it, so the corpus can be
// swept exhaustively without a Go-side expectation list to keep in step.
//
// The sweep runs length 0..40 with the difference at every position, in both
// operand orders, with a truncated `n`, and with both ranges offset one byte
// into a longer buffer. That range reaches the scalar remainder, both
// overlapping windows, one full 16-byte block and two, so every boundary a
// vector body can get wrong is swept on every backend.
//
// A failure returns a small distinct code rather than a count, so the exit
// status says WHICH shape disagreed. 42 means every comparison matched.
const mismatchIRProg = `function ref(a: string, ao: i32, b: string, bo: i32, n: i32): i32 {
    var xa: i32 = ao;
    if (xa < 0) { xa = 0; }
    if (xa > a.len()) { xa = a.len(); }
    var xb: i32 = bo;
    if (xb < 0) { xb = 0; }
    if (xb > b.len()) { xb = b.len(); }
    var m: i32 = n;
    if (m > a.len() - xa) { m = a.len() - xa; }
    if (m > b.len() - xb) { m = b.len() - xb; }
    if (m < 0) { m = 0; }
    var i: i32 = 0;
    while (i < m) {
        if (a[xa + i] != b[xb + i]) { return i; }
        i = i + 1;
    }
    return m;
}
function rep(n: i32): string {
    var s: string = "";
    var i: i32 = 0;
    while (i < n) { s = s + "a"; i = i + 1; }
    return s;
}
function main(): i32 {
    var n: i32 = 0;
    while (n <= 40) {
        var a: string = rep(n);
        if (__mismatch(a, 0, a, 0, n) != ref(a, 0, a, 0, n)) { return 1; }
        var d: i32 = 0;
        while (d < n) {
            var b: string = rep(d) + "z" + rep(n - d - 1);
            if (__mismatch(a, 0, b, 0, n) != ref(a, 0, b, 0, n)) { return 2; }
            if (__mismatch(b, 0, a, 0, n) != ref(b, 0, a, 0, n)) { return 3; }
            if (__mismatch(a, 0, b, 0, d) != ref(a, 0, b, 0, d)) { return 4; }
            var pa: string = "q" + a;
            var pb: string = "q" + b;
            if (__mismatch(pa, 1, pb, 1, n) != ref(pa, 1, pb, 1, n)) { return 5; }
            d = d + 1;
        }
        n = n + 1;
    }
    if (__mismatch("ab", 0, "abcdef", 0, 6) != 2) { return 6; }
    if (__mismatch("abc", 0 - 4, "abc", 0, 3) != 3) { return 7; }
    if (__mismatch("abc", 99, "abc", 0, 3) != 0) { return 8; }
    if (__mismatch("zzabc", 2, "qqqabd", 3, 3) != 2) { return 9; }
    if (__mismatch("abc", 0, "abd", 0, 2) != 2) { return 10; }
    if (__mismatch("", 0, "", 0, 0) != 0) { return 11; }
    if (__mismatch("abc", 0, "", 0, 3) != 0) { return 12; }
    return 42;
}
`

// runMismatchIR compiles mismatchIRProg with the self-host modload driver for
// the given register target and returns the exit code.
func runMismatchIR(t *testing.T, target string) int {
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

	progAsm, progDir := compileSourceModload(t, runner, driverBin, mismatchIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "mismatch_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostMismatchIRX86_64(t *testing.T) {
	if got := runMismatchIR(t, "x86-64-linux"); got != 42 {
		t.Errorf("__mismatch self-host x86-64 = %d, want 42 (see mismatchIRProg for what each code means)", got)
	}
}

func TestSelfHostMismatchIRArm64(t *testing.T) {
	if got := runMismatchIR(t, "arm64-linux"); got != 42 {
		t.Errorf("__mismatch self-host arm64 = %d, want 42 (see mismatchIRProg for what each code means)", got)
	}
}

// TestSelfHostMismatchIRWasm runs the same program through the self-hosted wasm
// IR driver. This leg is NOT redundant with the two above: the self-host wasm
// string is a single `[len@0][bytes@4]` block, so both operands always have an
// address and this tier runs a v128 body where the native wasm backend keeps a
// scalar one for its two-word SSO strings.
//
// The op lowers to a call rather than inline code: the body needs a loop over
// its own locals, and the operand stack here is the wasm value stack. The
// helper's presence in the emitted text is asserted, so a module that silently
// stopped needing it would fail rather than pass by not exercising anything.
func TestSelfHostMismatchIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host mismatch wasm IR e2e")
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
	cmd.Stdin = strings.NewReader(mismatchIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_mismatch")) {
		t.Fatal("emitted wat has no $__fern_mismatch helper — the op did not lower")
	}
	watFile := filepath.Join(dir, "mismatch.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 42 {
		t.Errorf("__mismatch self-host wasm = %d, want 42 (see mismatchIRProg for what each code means)", code)
	}
}
