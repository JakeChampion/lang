package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__bsd_sum(s, sum)` on the self-host IR path: the BSD checksum `sum -r`
// keeps, continued over s from `sum` — per byte, rotate the 16 bits right by
// one and add the byte, modulo 2^16.

// bsdSumIRProg is SELF-CHECKING: it carries a Fern reference and compares the
// kernel against it over every length to 40 from several starting sums, and
// over 300 high bytes. 42 means every comparison matched.
const bsdSumIRProg = `function ref(s: string, sum: i32): i32 {
    var v: i32 = sum & 65535;
    var i: i32 = 0;
    while (i < s.len()) {
        v = ((v >> 1) | (v << 15)) & 65535;
        v = (v + (s[i] as i32)) & 65535;
        i = i + 1;
    }
    return v;
}
function main(): i32 {
    var n: i32 = 0;
    var s: string = "";
    while (n <= 40) {
        if (__bsd_sum(s, 0) != ref(s, 0)) { return 1; }
        if (__bsd_sum(s, 1) != ref(s, 1)) { return 2; }
        if (__bsd_sum(s, 32768) != ref(s, 32768)) { return 3; }
        if (__bsd_sum(s, 65535) != ref(s, 65535)) { return 4; }
        s = s + chr((n * 37 + 11) % 128);
        n = n + 1;
    }
    var high: string = "";
    var k: i32 = 0;
    while (k < 300) { high = high + "\xff\x80"; k = k + 1; }
    if (__bsd_sum(high, 4660) != ref(high, 4660)) { return 5; }
    if (__bsd_sum("abc", 74565) != ref("abc", 74565)) { return 6; }
    return 42;
}
`

// runBsdSumIR compiles bsdSumIRProg with the self-host modload driver for
// the given register target and returns the exit code.
func runBsdSumIR(t *testing.T, target string) int {
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

	progAsm, progDir := compileSourceModload(t, runner, driverBin, bsdSumIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "bsd_sum_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostBsdSumIRX86_64(t *testing.T) {
	if got := runBsdSumIR(t, "x86-64-linux"); got != 42 {
		t.Errorf("__bsd_sum self-host x86-64 = %d, want 42 (see bsdSumIRProg for what each code means)", got)
	}
}

func TestSelfHostBsdSumIRArm64(t *testing.T) {
	if got := runBsdSumIR(t, "arm64-linux"); got != 42 {
		t.Errorf("__bsd_sum self-host arm64 = %d, want 42 (see bsdSumIRProg for what each code means)", got)
	}
}

// TestSelfHostBsdSumIRWasm runs the same program through the self-hosted
// wasm IR driver. The helper's presence in the emitted text is asserted, so a
// module that silently stopped needing it would fail rather than pass by
// exercising nothing.
func TestSelfHostBsdSumIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host bsd_sum wasm IR e2e")
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
	cmd.Stdin = strings.NewReader(bsdSumIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_bsd_sum")) {
		t.Fatal("emitted wat has no $__fern_bsd_sum helper — the op did not lower")
	}
	watFile := filepath.Join(dir, "bsd_sum.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_, _ = run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if got := run.ProcessState.ExitCode(); got != 42 {
		t.Errorf("__bsd_sum self-host wasm = %d, want 42 (see bsdSumIRProg for what each code means)", got)
	}
}
