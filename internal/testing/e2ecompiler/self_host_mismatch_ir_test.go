package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const mismatchIRProg = `function ref(a: string, ao: i32, b: string, bo: i32, n: i32): i32 {
    let xa: i32 = ao;
    if (xa < 0) { xa = 0; }
    if (xa > a.len()) { xa = a.len(); }
    let xb: i32 = bo;
    if (xb < 0) { xb = 0; }
    if (xb > b.len()) { xb = b.len(); }
    let m: i32 = n;
    if (m > a.len() - xa) { m = a.len() - xa; }
    if (m > b.len() - xb) { m = b.len() - xb; }
    if (m < 0) { m = 0; }
    let i: i32 = 0;
    while (i < m) {
        if (a[xa + i] != b[xb + i]) { return i; }
        i = i + 1;
    }
    return m;
}
function rep(n: i32): string {
    let s: string = "";
    let i: i32 = 0;
    while (i < n) { s = s + "a"; i = i + 1; }
    return s;
}
function main(): i32 {
    let n: i32 = 0;
    while (n <= 40) {
        let a: string = rep(n);
        if (__mismatch(a, 0, a, 0, n) != ref(a, 0, a, 0, n)) { return 1; }
        let d: i32 = 0;
        while (d < n) {
            let b: string = rep(d) + "z" + rep(n - d - 1);
            if (__mismatch(a, 0, b, 0, n) != ref(a, 0, b, 0, n)) { return 2; }
            if (__mismatch(b, 0, a, 0, n) != ref(b, 0, a, 0, n)) { return 3; }
            if (__mismatch(a, 0, b, 0, d) != ref(a, 0, b, 0, d)) { return 4; }
            let pa: string = "q" + a;
            let pb: string = "q" + b;
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

func TestSelfHostMismatchIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host mismatch wasm IR e2e")
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
