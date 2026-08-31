package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__rmemchr(s, byte, from)` on the self-host IR path — the third fused kernel
// of docs/ATLAS-PLATFORM-PLAN.md §3, and the backward sibling §3.3 nominates.
//
// It lands SCALAR on all seven backends at once, which is §3.4's step 1: the
// intrinsic becomes total before anything depends on it being fast, so no step
// can leave the tree unbuildable. That ordering is also what let __memchr's
// own corpus survive its vectorisation unchanged — it was written while every
// body was still a byte loop, so it already covered the block boundaries the
// vector bodies would later have.
//
// The one thing that is NOT a mirror image of the forward scan, and so the one
// thing a seventh port gets wrong, is the clamp: `from` clamps DOWN to len-1
// here where __memchr clamps UP to 0. A negative `from` therefore finds
// nothing, where in __memchr it means "search the whole string".

// rmemchrIRProg is SELF-CHECKING: it carries its own reference implementation
// in Fern and compares `__rmemchr` against it, so the corpus sweeps
// exhaustively with no Go-side expectation list to keep in step.
//
// Lengths 0..40 with the needle at every position and the scan starting
// before / at / after it — two full 16-byte blocks plus a partial tail either
// side, so the boundaries a vector body will later have are already covered.
//
// A failure returns a small distinct code rather than a count, so the exit
// status says WHICH shape disagreed. 42 means every comparison matched.
const rmemchrIRProg = `function ref(s: string, b: i32, from: i32): i32 {
    if (b < 0) { return 0 - 1; }
    if (b > 255) { return 0 - 1; }
    var i: i32 = from;
    if (i > s.len() - 1) { i = s.len() - 1; }
    while (i >= 0) {
        if ((s[i] as i32) == b) { return i; }
        i = i - 1;
    }
    return 0 - 1;
}
function main(): i32 {
    var n: i32 = 0;
    while (n <= 40) {
        var base: string = "";
        var k: i32 = 0;
        while (k < n) { base = base + "a"; k = k + 1; }
        if (__rmemchr(base, 122, n) != ref(base, 122, n)) { return 1; }
        var at: i32 = 0;
        while (at < n) {
            var s: string = slice_unchecked(base, 0, at) + "z" + slice_unchecked(base, at + 1, n);
            if (__rmemchr(s, 122, n) != ref(s, 122, n)) { return 2; }
            if (__rmemchr(s, 122, at) != ref(s, 122, at)) { return 3; }
            if (__rmemchr(s, 122, at - 1) != ref(s, 122, at - 1)) { return 4; }
            if (__rmemchr(s, 97, at) != ref(s, 97, at)) { return 5; }
            at = at + 1;
        }
        n = n + 1;
    }
    // A from past the end clamps to the last index rather than missing.
    if (__rmemchr("abc", 97, 100) != 0) { return 6; }
    // ... and a negative one finds nothing, which is where this parts company
    // with __memchr, whose negative from means the whole string.
    if (__rmemchr("abc", 97, 0 - 1) != 0 - 1) { return 7; }
    if (__rmemchr("abc", 256, 2) != 0 - 1) { return 8; }
    if (__rmemchr("abc", 0 - 1, 2) != 0 - 1) { return 9; }
    if (__rmemchr("", 97, 0) != 0 - 1) { return 10; }
    // RIGHTMOST, not leftmost — the property the whole op exists for, and the
    // one a copy of __memchr's body would still pass every sweep above with.
    if (__rmemchr("aba", 97, 2) != 2) { return 11; }
    if (__rmemchr("aba", 97, 1) != 0) { return 12; }
    return 42;
}
`

// runRmemchrIR compiles rmemchrIRProg with the self-host modload driver for
// the given register target and returns the exit code.
func runRmemchrIR(t *testing.T, target string) int {
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

	progAsm, progDir := compileSourceModload(t, runner, driverBin, rmemchrIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "rmemchr_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostRmemchrIRX86_64(t *testing.T) {
	if got := runRmemchrIR(t, "x86-64-linux"); got != 42 {
		t.Errorf("__rmemchr self-host x86-64 = %d, want 42 (see rmemchrIRProg for what each code means)", got)
	}
}

func TestSelfHostRmemchrIRArm64(t *testing.T) {
	if got := runRmemchrIR(t, "arm64-linux"); got != 42 {
		t.Errorf("__rmemchr self-host arm64 = %d, want 42 (see rmemchrIRProg for what each code means)", got)
	}
}

// TestSelfHostRmemchrIRWasm runs the same program through the self-hosted wasm
// IR driver. The helper's presence in the emitted text is asserted, so a module
// that silently stopped needing it would fail rather than pass by exercising
// nothing.
func TestSelfHostRmemchrIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host rmemchr wasm IR e2e")
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
	cmd.Stdin = strings.NewReader(rmemchrIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_rmemchr")) {
		t.Fatal("emitted wat has no $__fern_rmemchr helper — the op did not lower")
	}
	watFile := filepath.Join(dir, "rmemchr.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 42 {
		t.Errorf("__rmemchr self-host wasm = %d, want 42 (see rmemchrIRProg for what each code means)", code)
	}
}
