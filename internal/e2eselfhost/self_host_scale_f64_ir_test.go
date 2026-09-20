package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__scale_f64(xs, k)` on the self-host IR path — the eighth fused kernel of
// docs/ATLAS-PLATFORM-PLAN.md §3, the first over an ARRAY and the first with a
// buffer out.
//
// This is the leg the Go-side suite cannot stand in for. `internal/e2e`'s
// differential proves the NATIVE emitters; the self-host op, its two native
// assembly bodies and its wat helper are a separate implementation over a
// DIFFERENT array layout (len at [arr], elements from [arr+8]) that no Go
// test reaches.
//
// What a port of a buffer-out kernel gets wrong is the HEADER, not the
// arithmetic: the result's length, which every later read consults, and the
// element base. The sweep therefore reads every element back through the
// ordinary indexing path and checks `len()`, on lengths 0..40 and past the
// two- and four-lane boundaries a vector body will have.

// scaleF64IRProg is SELF-CHECKING: it carries its own reference in Fern and
// compares `__scale_f64` against it. A failure returns a small distinct code,
// so the exit status says WHICH shape disagreed. 42 means every comparison
// matched.
const scaleF64IRProg = `function build(n: i32, seed: f64): f64[] {
    var xs: f64[] = [];
    var i: i32 = 0;
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
    var got: f64[] = __scale_f64(xs, k);
    if (got.len() != xs.len()) { return 1; }
    var i: i32 = 0;
    while (i < xs.len()) {
        if (!same(got[i], xs[i] * k)) { return 2; }
        i = i + 1;
    }
    return 0;
}
function main(): i32 {
    var ks: f64[] = [2.5, 0.0 - 0.5, 0.0, 1.0, 3.0e300, 0.0 / 0.0];
    var n: i32 = 0;
    while (n <= 40) {
        var xs: f64[] = build(n, 0.5);
        var j: i32 = 0;
        while (j < ks.len()) {
            var code: i32 = check(xs, ks[j]);
            if (code != 0) { return 10 + code; }
            j = j + 1;
        }
        n = n + 1;
    }
    var big: i32[] = [63, 64, 65, 129, 1000];
    var b: i32 = 0;
    while (b < big.len()) {
        var code: i32 = check(build(big[b], 0.0 - 100.0), 2.5);
        if (code != 0) { return 20 + code; }
        b = b + 1;
    }
    // The input is borrowed: it reads the same after the call, and the
    // result is a different buffer.
    var v: f64[] = build(5, 1.0);
    var w: f64[] = __scale_f64(v, 3.0);
    if (!same(v[2], 1.0 + 2.5 - 7.0) || !same(w[2], (1.0 + 2.5 - 7.0) * 3.0)) { return 30; }
    var w2: f64[] = w.with(0, 99.0);
    if (!same(v[0], 1.0 - 7.0) || w2[0] != 99.0) { return 31; }
    return 42;
}
`

// runScaleF64IR compiles scaleF64IRProg with the self-host modload driver for
// the given register target and returns the exit code.
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
	progBin := buildBin(t, linkGcc, progDir, "scale_f64_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
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

// TestSelfHostScaleF64IRWasm runs the same program through the self-hosted
// wasm IR driver. The helper's presence in the emitted text is asserted, so a
// module that silently stopped needing it would fail rather than pass by
// exercising nothing.
func TestSelfHostScaleF64IRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host scale_f64 wasm IR e2e")
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
