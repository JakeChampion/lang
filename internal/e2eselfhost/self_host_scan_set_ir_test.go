package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__scan_set(s, from, set)` on the self-host IR path: the index of the first
// byte at or after `from` whose entry in the u8[] `set` is nonzero, or the
// LENGTH. The byte-set scan behind wc's word split, cat -A's spelling and
// tr -d.
//
// What a port gets wrong here is the SET: its length lives in the array
// header and its bytes in the backend's element slots — eight bytes on the
// register backends, four on wasm — so a body that reads the set as a byte
// string, or that forgets a byte past a short set's end is not a member,
// answers the dense cases right and fails the sparse and short ones. The
// sweep below holds both, plus the cursor at every offset and the clamp.

// scanSetIRProg is SELF-CHECKING: it carries its own reference implementation
// in Fern and compares `__scan_set` against it, so the corpus sweeps with no
// Go-side expectation list to keep in step. A failure returns a small
// distinct code rather than an index, so the exit status says WHICH shape
// disagreed. 42 means every comparison matched.
const scanSetIRProg = `function ref(s: string, from: i32, set: u8[]): i32 {
    var i: i32 = from;
    if (i < 0) { i = 0; }
    while (i < s.len()) {
        var b: i32 = s[i] as i32;
        if (b < set.len() && set[b] as i32 != 0) { return i; }
        i = i + 1;
    }
    return s.len();
}
function space_set(): u8[] {
    var set: u8[] = __alloc_u8(256);
    var z: i32 = 0;
    while (z < 256) { set = set.with(z, 0 as u8); z = z + 1; }
    set = set.with(32, 1 as u8);
    set = set.with(9, 1 as u8);
    set = set.with(10, 1 as u8);
    return set;
}
function main(): i32 {
    var space: u8[] = space_set();
    var short: u8[] = [0 as u8, 1 as u8, 0 as u8, 1 as u8];
    var n: i32 = 0;
    while (n <= 40) {
        var base: string = "";
        var k: i32 = 0;
        while (k < n) { base = base + "a"; k = k + 1; }
        var from: i32 = 0 - 1;
        while (from <= n + 1) {
            if (__scan_set(base, from, space) != ref(base, from, space)) { return 1; }
            from = from + 1;
        }
        var at: i32 = 0;
        while (at < n) {
            var s: string = slice_unchecked(base, 0, at) + " " + slice_unchecked(base, at + 1, n);
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
    var none: u8[] = [];
    if (__scan_set("abc", 0, none) != 3) { return 15; }
    var high: u8[] = space_set();
    high = high.with(255, 1 as u8);
    if (__scan_set("\x7f\xfe\xff", 0, high) != 2) { return 16; }
    if (__scan_set("\x7f\xfe\xff", 3, high) != 3) { return 17; }
    return 42;
}
`

// runScanSetIR compiles scanSetIRProg with the self-host modload driver for
// the given register target and returns the exit code.
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

// TestSelfHostScanSetIRWasm runs the same program through the self-hosted
// wasm IR driver. The helper's presence in the emitted text is asserted, so a
// module that silently stopped needing it would fail rather than pass by
// exercising nothing.
func TestSelfHostScanSetIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host scan_set wasm IR e2e")
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
