package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const asciiRunIRProg = `function ref(s: string, from: i32): i32 {
    let i: i32 = from;
    if (i < 0) { i = 0; }
    while (i < s.len()) {
        if ((s[i] as i32) >= 128) { return i; }
        i = i + 1;
    }
    return s.len();
}
function main(): i32 {
    let n: i32 = 0;
    while (n <= 72) {
        let base: string = "";
        let k: i32 = 0;
        while (k < n) { base = base + "a"; k = k + 1; }
        if (__ascii_run(base, 0) != ref(base, 0)) { return 1; }
        let at: i32 = 0;
        while (at < n) {
            let s: string = slice_unchecked(base, 0, at) + string_from_bytes_unchecked([195 as u8]) + slice_unchecked(base, at + 1, n);
            if (__ascii_run(s, 0) != ref(s, 0)) { return 2; }
            if (__ascii_run(s, at) != ref(s, at)) { return 3; }
            if (__ascii_run(s, at + 1) != ref(s, at + 1)) { return 4; }
            at = at + 1;
        }
        n = n + 1;
    }
    // Every byte >= 0x80 is a hit, not just UTF-8 lead bytes: 0x80 is a bare
    // continuation and 0xFF is not legal UTF-8 at all. This intrinsic answers
    // "not ASCII", not "start of a codepoint".
    if (__ascii_run("abc" + string_from_bytes_unchecked([128 as u8]) + "def", 0) != 3) { return 5; }
    if (__ascii_run("abc" + string_from_bytes_unchecked([255 as u8]) + "def", 0) != 3) { return 6; }
    if (__ascii_run("abc" + string_from_bytes_unchecked([191 as u8]) + "def", 0) != 3) { return 7; }
    // 0x7F is the last ASCII byte and must NOT be a hit — the off-by-one an
    // inclusive/exclusive slip on the high-bit test produces.
    if (__ascii_run("\x7f\x7f\x7f", 0) != 3) { return 8; }
    // The miss value is the length, on every shape that can miss.
    if (__ascii_run("", 0) != 0) { return 9; }
    if (__ascii_run("abc", 100) != 3) { return 10; }
    if (__ascii_run("abc", 0 - 5) != 3) { return 11; }
    // A hit at index 0, and one reached only after a whole vector block.
    if (__ascii_run(string_from_bytes_unchecked([255 as u8]) + "abc", 0) != 0) { return 12; }
    if (__ascii_run("aaaaaaaaaaaaaaaa" + string_from_bytes_unchecked([195 as u8]), 0) != 16) { return 13; }
    return 42;
}
`

func runAsciiRunIR(t *testing.T, target string) int {
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

	progAsm, progDir := compileSourceModload(t, runner, driverBin, asciiRunIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "ascii_run_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostAsciiRunIRX86_64(t *testing.T) {
	if got := runAsciiRunIR(t, "x86-64-linux"); got != 42 {
		t.Errorf("__ascii_run self-host x86-64 = %d, want 42 (see asciiRunIRProg for what each code means)", got)
	}
}

func TestSelfHostAsciiRunIRArm64(t *testing.T) {
	if got := runAsciiRunIR(t, "arm64-linux"); got != 42 {
		t.Errorf("__ascii_run self-host arm64 = %d, want 42 (see asciiRunIRProg for what each code means)", got)
	}
}

func TestSelfHostAsciiRunIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host ascii_run wasm IR e2e")
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
	cmd.Stdin = strings.NewReader(asciiRunIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_ascii_run")) {
		t.Fatal("emitted wat has no $__fern_ascii_run helper — the op did not lower")
	}
	watFile := filepath.Join(dir, "ascii_run.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 42 {
		t.Errorf("__ascii_run self-host wasm = %d, want 42 (see asciiRunIRProg for what each code means)", code)
	}
}
