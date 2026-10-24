package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

const withCloneReclaimSrc = `struct Box { mag: u64[] }

@noinline
function churn_field(b: Box, n: i32): u64 {
    let cur: u64[] = b.mag;
    let i: i32 = 0;
    while (i < n) {
        cur = cur.with(i % 4, (i as u64) + (1 as u64));
        i = i + 1;
    }
    return cur[0] + cur[1] + cur[2] + cur[3];
}

@noinline
function churn_param(a: u64[], n: i32): u64 {
    let cur: u64[] = a;
    let i: i32 = 0;
    while (i < n) {
        cur = cur.with(i % 4, (i as u64) + (1 as u64));
        i = i + 1;
    }
    return cur[0] + cur[1] + cur[2] + cur[3];
}

function seed(): u64[] {
    let m: u64[] = [];
    let i: i32 = 0;
    while (i < 4) { m = m.append(7 as u64); i = i + 1; }
    return m;
}

function main(): i32 {
    let mf: u64[] = seed();
    let b: Box = Box { mag: mf };
    let sf: u64 = churn_field(b, 100);
    if (b.mag[0] + b.mag[1] + b.mag[2] + b.mag[3] != (28 as u64)) { return 1; }
    let mp: u64[] = seed();
    let sp: u64 = churn_param(mp, 100);
    if (mp[0] + mp[1] + mp[2] + mp[3] != (28 as u64)) { return 2; }
    if (sf != sp) { return 3; }
    if (__rc_underflow_count() != 0) { return 99; }
    return (sf as i32) - 387;
}`

const withCloneReclaimExit = 7

func TestSelfHostWithCloneReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	interp := buildLangBinForInterp(t)
	if got := interpExit(t, interp, withCloneReclaimSrc); got != withCloneReclaimExit {
		t.Fatalf("interpreter = %d, want %d", got, withCloneReclaimExit)
	}
	asm := hevCompile(t, runner, driverBin, withCloneReclaimSrc, []string{"FERN_LEAKCHECK=1"})
	bin := buildBin(t, gcc, dir, "with_clone_reclaim", asm)
	stderr, exit := hevRun(t, runner, bin)
	if exit != withCloneReclaimExit {
		t.Fatalf("exit = %d, want %d — the release freed a live buffer?\n%s", exit, withCloneReclaimExit, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostWithCloneReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")
	asm := string(runCaptureEnv(t, x86runner, driverBin, []byte(withCloneReclaimSrc),
		[]string{"PATH=/usr/bin:/bin", "FERN_LEAKCHECK=1"}, "-target", "arm64-linux"))
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "with_clone_reclaim_arm64", asm)
	cmd := runArm64Bin(qemu, bin)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != withCloneReclaimExit {
		t.Fatalf("exit = %d, want %d — the release freed a live buffer?\n%s", code, withCloneReclaimExit, errBuf.String())
	}
	assertBalancedCensus(t, errBuf.String())
}

func TestSelfHostWithCloneReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm .with clone reclaim e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "wasmdriver")
	wat := wasmLcCompile(t, runner, driverBin, withCloneReclaimSrc, []string{"FERN_LEAKCHECK=1"})
	stderr, exit := wasmLcRun(t, dir, "with_clone_reclaim", wat)
	if exit != withCloneReclaimExit {
		t.Fatalf("exit = %d, want %d — the release freed a live buffer?\n%s", exit, withCloneReclaimExit, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func assertBalancedCensus(t *testing.T, stderr string) {
	t.Helper()
	summary := leakSummaryLine(stderr)
	if summary == "" {
		t.Fatalf("no leakcheck summary on stderr: %q", stderr)
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if allocs == 0 {
		t.Fatal("allocs=0 — the probe exercised no allocation")
	}
	if allocs != frees || live != 0 {
		t.Errorf("%s — want a balanced census", summary)
	}
}
