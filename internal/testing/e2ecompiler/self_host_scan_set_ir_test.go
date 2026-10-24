package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const scanSetIRProg = `function ref(s: string, from: i32, set: u8[]): i32 {
    let i: i32 = from;
    if (i < 0) { i = 0; }
    while (i < s.len()) {
        let b: i32 = s[i] as i32;
        if (b < set.len() && set[b] as i32 != 0) { return i; }
        i = i + 1;
    }
    return s.len();
}
function space_set(): u8[] {
    let set: u8[] = __alloc_u8(256);
    let z: i32 = 0;
    while (z < 256) { set = set.with(z, 0 as u8); z = z + 1; }
    set = set.with(32, 1 as u8);
    set = set.with(9, 1 as u8);
    set = set.with(10, 1 as u8);
    return set;
}
function main(): i32 {
    let space: u8[] = space_set();
    let short: u8[] = [0 as u8, 1 as u8, 0 as u8, 1 as u8];
    let n: i32 = 0;
    while (n <= 40) {
        let base: string = "";
        let k: i32 = 0;
        while (k < n) { base = base + "a"; k = k + 1; }
        let from: i32 = 0 - 1;
        while (from <= n + 1) {
            if (__scan_set(base, from, space) != ref(base, from, space)) { return 1; }
            from = from + 1;
        }
        let at: i32 = 0;
        while (at < n) {
            let s: string = slice_unchecked(base, 0, at) + " " + slice_unchecked(base, at + 1, n);
            if (__scan_set(s, 0, space) != ref(s, 0, space)) { return 2; }
            if (__scan_set(s, at, space) != ref(s, at, space)) { return 3; }
            if (__scan_set(s, at + 1, space) != ref(s, at + 1, space)) { return 4; }
            at = at + 1;
        }
        n = n + 1;
    }
    if (__scan_set("the quick\tbrown\nfox", 0, space) != 3) { return 5; }
    if (__scan_set("the quick\tbrown\nfox", 4, space) != 9) { return 6; }
    if (__scan_set("the quick\tbrown\nfox", 10, space) != 15) { return 7; }
    if (__scan_set("the quick\tbrown\nfox", 16, space) != 19) { return 8; }
    if (__scan_set("", 0, space) != 0) { return 9; }
    if (__scan_set("abc", 0, short) != 3) { return 10; }
    if (__scan_set("\x01\x03\x02", 0, short) != 0) { return 11; }
    if (__scan_set("\x00\x02\x01", 0, short) != 2) { return 12; }
    if (__scan_set("\x03\x03\x03", 0, short) != 0) { return 13; }
    if (__scan_set("\x00b\x00", 0, short) != 3) { return 14; }
    let none: u8[] = [];
    if (__scan_set("abc", 0, none) != 3) { return 15; }
    let high: u8[] = space_set();
    high = high.with(255, 1 as u8);
    if (__scan_set("\x7f" + string_from_bytes_unchecked([254 as u8, 255 as u8]), 0, high) != 2) { return 16; }
    if (__scan_set("\x7f" + string_from_bytes_unchecked([254 as u8, 255 as u8]), 3, high) != 3) { return 17; }
    return 42;
}
`

func runScanSetIR(t *testing.T, target string) int {
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

	progAsm, progDir := compileSourceModload(t, runner, driverBin, scanSetIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "scan_set_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostScanSetIRX86_64(t *testing.T) {
	if got := runScanSetIR(t, "x86-64-linux"); got != 42 {
		t.Errorf("__scan_set self-host x86-64 = %d, want 42 (see scanSetIRProg for what each code means)", got)
	}
}

func TestSelfHostScanSetIRArm64(t *testing.T) {
	if got := runScanSetIR(t, "arm64-linux"); got != 42 {
		t.Errorf("__scan_set self-host arm64 = %d, want 42 (see scanSetIRProg for what each code means)", got)
	}
}

func TestSelfHostScanSetIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host scan_set wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = strings.NewReader(scanSetIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_scan_set")) {
		t.Fatal("emitted wat has no $__fern_scan_set helper — the op did not lower")
	}
	watFile := filepath.Join(dir, "scan_set.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_, _ = run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if got := run.ProcessState.ExitCode(); got != 42 {
		t.Errorf("__scan_set self-host wasm = %d, want 42 (see scanSetIRProg for what each code means)", got)
	}
}
