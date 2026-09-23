package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__alloc_u8(n)` is a fresh u8[] of length n, every element zero (#10081).
// The self-host runtime builds it on __fern_arr_box, which may hand back a
// block off the free list with its last owner's bytes still in it, so the
// program fills a table, drops it, and asks for one of the same size and of
// other sizes, over and over: any stale byte fails. 42 means every element
// of every fresh array read zero; the other codes name the size that did not.
const allocU8ZeroIRProg = `function filled(n: i32): u8[] {
    var t: u8[] = __alloc_u8(n);
    var b: i32 = 0;
    while (b < n) {
        t = t.with(b, 255 as u8);
        b = b + 1;
    }
    return t;
}
function nonzero(t: u8[]): i32 {
    var b: i32 = 0;
    while (b < t.len()) {
        if (t[b] as i32 != 0) { return 1; }
        b = b + 1;
    }
    return 0;
}
function main(): i32 {
    var sizes: i32[] = [256, 1, 7, 64, 300];
    var k: i32 = 0;
    while (k < 20) {
        var s: i32 = 0;
        while (s < sizes.len()) {
            var n: i32 = sizes[s];
            var a: u8[] = filled(n);
            var keep: i32 = a[n - 1] as i32;
            var z: u8[] = __alloc_u8(n);
            if (z.len() != n) { return 10 + s; }
            if (nonzero(z) != 0) { return 20 + s; }
            if (keep != 255) { return 30 + s; }
            s = s + 1;
        }
        k = k + 1;
    }
    return 42;
}
`

func runAllocU8ZeroIR(t *testing.T, target string) int {
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
	progAsm, progDir := compileSourceModload(t, runner, driverBin, allocU8ZeroIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "alloc_u8_zero_ir", progAsm)
	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostAllocU8ZeroIRX86_64(t *testing.T) {
	if got := runAllocU8ZeroIR(t, "x86-64-linux"); got != 42 {
		t.Errorf("__alloc_u8 self-host x86-64 = %d, want 42 (see allocU8ZeroIRProg for what each code means)", got)
	}
}

func TestSelfHostAllocU8ZeroIRArm64(t *testing.T) {
	if got := runAllocU8ZeroIR(t, "arm64-linux"); got != 42 {
		t.Errorf("__alloc_u8 self-host arm64 = %d, want 42 (see allocU8ZeroIRProg for what each code means)", got)
	}
}

func TestSelfHostAllocU8ZeroIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host alloc_u8 wasm IR e2e")
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
	cmd.Stdin = strings.NewReader(allocU8ZeroIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "alloc_u8_zero.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_, _ = run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if got := run.ProcessState.ExitCode(); got != 42 {
		t.Errorf("__alloc_u8 self-host wasm = %d, want 42 (see allocU8ZeroIRProg for what each code means)", got)
	}
}
