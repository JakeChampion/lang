package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// The aliased/borrowed `a = a.with(i, v)` self-rebind lowers to a CLONE of the
// receiver plus a store (#3599: an in-place element write would mutate the
// buffer the other owner still reads). The store gave the superseded buffer no
// release, so a `.with` in a loop allocated one clone per iteration and freed
// none — 200 unpaired blocks here, and 240k blocks / 467 MB on `od -t fL` over
// 48 arbitrary bytes, whose 80-bit decimal expansion runs this shape through
// core/bigint's `__bi_mul_small` and `BigInt.to_string`.
//
// Both shapes the compiler-sized case exercises are here: a local bound from a
// struct FIELD read (to_string's `var cur = a.mag`) and one bound from a
// borrowed array PARAM (mul_small's `var out = a`). Each source is read back
// after the loop, so a release that freed the live buffer, or a clone that
// wrote through to it, is an answer this program reports rather than a leak
// the census alone would have to catch.
const withCloneReclaimSrc = `struct Box { mag: u64[] }

@noinline
function churn_field(b: Box, n: i32): u64 {
    var cur: u64[] = b.mag;
    var i: i32 = 0;
    while (i < n) {
        cur = cur.with(i % 4, (i as u64) + (1 as u64));
        i = i + 1;
    }
    return cur[0] + cur[1] + cur[2] + cur[3];
}

@noinline
function churn_param(a: u64[], n: i32): u64 {
    var cur: u64[] = a;
    var i: i32 = 0;
    while (i < n) {
        cur = cur.with(i % 4, (i as u64) + (1 as u64));
        i = i + 1;
    }
    return cur[0] + cur[1] + cur[2] + cur[3];
}

function seed(): u64[] {
    var m: u64[] = [];
    var i: i32 = 0;
    while (i < 4) { m = m.append(7 as u64); i = i + 1; }
    return m;
}

function main(): i32 {
    var mf: u64[] = seed();
    var b: Box = Box { mag: mf };
    var sf: u64 = churn_field(b, 100);
    if (b.mag[0] + b.mag[1] + b.mag[2] + b.mag[3] != (28 as u64)) { return 1; }
    var mp: u64[] = seed();
    var sp: u64 = churn_param(mp, 100);
    if (mp[0] + mp[1] + mp[2] + mp[3] != (28 as u64)) { return 2; }
    if (sf != sp) { return 3; }
    if (__rc_underflow_count() != 0) { return 99; }
    return (sf as i32) - 387;
}`

// The last four writes leave 97+98+99+100; both sources stay at 4x7.
const withCloneReclaimExit = 7

func TestSelfHostWithCloneReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
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
	// 205 / 5 at live 11200 before the credit: one clone per iteration, none
	// released.
	assertBalancedCensus(t, stderr)
}

func TestSelfHostWithCloneReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
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
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasmdriver")
	wat := wasmLcCompile(t, runner, driverBin, withCloneReclaimSrc, []string{"FERN_LEAKCHECK=1"})
	stderr, exit := wasmLcRun(t, dir, "with_clone_reclaim", wat)
	if exit != withCloneReclaimExit {
		t.Fatalf("exit = %d, want %d — the release freed a live buffer?\n%s", exit, withCloneReclaimExit, stderr)
	}
	assertBalancedCensus(t, stderr)
}

// assertBalancedCensus reads the leakcheck summary off a run's stderr and
// requires allocs == frees at live 0 with allocation having happened.
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
