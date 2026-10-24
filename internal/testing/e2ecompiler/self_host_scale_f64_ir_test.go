package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const scaleF64IRProg = `function build(n: i32, seed: f64): f64[] {
    let xs: f64[] = [];
    let i: i32 = 0;
    while (i < n) {
        xs = xs.append(seed + (i as f64) * 1.25 - 7.0);
        i = i + 1;
    }
    return xs;
}
function same(a: f64, b: f64): boolean {
    if (a == b) { return true; }
    return a != a && b != b;
}
function check(xs: f64[], k: f64): i32 {
    let got: f64[] = __scale_f64(xs, k);
    if (got.len() != xs.len()) { return 1; }
    let i: i32 = 0;
    while (i < xs.len()) {
        if (!same(got[i], xs[i] * k)) { return 2; }
        i = i + 1;
    }
    return 0;
}
function main(): i32 {
    let ks: f64[] = [2.5, 0.0 - 0.5, 0.0, 1.0, 3.0e300, 0.0 / 0.0];
    let n: i32 = 0;
    while (n <= 40) {
        let xs: f64[] = build(n, 0.5);
        let j: i32 = 0;
        while (j < ks.len()) {
            let code: i32 = check(xs, ks[j]);
            if (code != 0) { return 10 + code; }
            j = j + 1;
        }
        n = n + 1;
    }
    let big: i32[] = [63, 64, 65, 129, 1000];
    let b: i32 = 0;
    while (b < big.len()) {
        let code: i32 = check(build(big[b], 0.0 - 100.0), 2.5);
        if (code != 0) { return 20 + code; }
        b = b + 1;
    }
    // The input is borrowed: it reads the same after the call, and the
    // result is a different buffer.
    let v: f64[] = build(5, 1.0);
    let w: f64[] = __scale_f64(v, 3.0);
    if (!same(v[2], 1.0 + 2.5 - 7.0) || !same(w[2], (1.0 + 2.5 - 7.0) * 3.0)) { return 30; }
    let w2: f64[] = w.with(0, 99.0);
    if (!same(v[0], 1.0 - 7.0) || w2[0] != 99.0) { return 31; }
    return 42;
}
`

func runScaleF64IR(t *testing.T, target string) int {
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

	progAsm, progDir := compileSourceModload(t, runner, driverBin, scaleF64IRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	if want := scaleF64VectorMnemonic(target); want != "" && !strings.Contains(string(progAsm), want) {
		t.Errorf("emitted %s assembly does not contain %q: the kernel is lowering scalar again", target, want)
	}
	progBin := buildBin(t, linkGcc, progDir, "scale_f64_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func scaleF64VectorMnemonic(target string) string {
	switch target {
	case "x86-64-linux":
		return "mulpd"
	case "arm64-linux":
		return "fmul v0.2d"
	}
	return ""
}

func TestSelfHostScaleF64IRX86_64(t *testing.T) {
	if got := runScaleF64IR(t, "x86-64-linux"); got != 42 {
		t.Errorf("__scale_f64 self-host x86-64 = %d, want 42 (see scaleF64IRProg for what each code means)", got)
	}
}

func TestSelfHostScaleF64IRArm64(t *testing.T) {
	if got := runScaleF64IR(t, "arm64-linux"); got != 42 {
		t.Errorf("__scale_f64 self-host arm64 = %d, want 42 (see scaleF64IRProg for what each code means)", got)
	}
}

func TestSelfHostScaleF64IRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host scale_f64 wasm IR e2e")
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
	cmd.Stdin = strings.NewReader(scaleF64IRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_scale_f64")) {
		t.Fatal("emitted wat has no $__fern_scale_f64 helper — the op did not lower")
	}
	watFile := filepath.Join(dir, "scale_f64.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 42 {
		t.Errorf("__scale_f64 self-host wasm = %d, want 42 (see scaleF64IRProg for what each code means)", code)
	}
}
