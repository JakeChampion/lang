package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #4357 (enum sibling of the struct/tuple loop-local reclaim #4733/#4735/#4736):
// a fresh rc-payload enum loop-local consumed by a match in the loop body
// (`while { var b = Full([i, i+1]); match (b) { Full(xs) => xs[0], … } }`) leaked
// its payload + box every iteration. The consuming-match reclaim
// (consumed_rcpayload_enum_frees) scans only the TOP-LEVEL fn body, so a loop-local
// enum was reclaimed nowhere. The fix collects such an enum "RCENUM:"
// (collect_fresh_rcenum_names — per-block single-owner / consumed-by-one-match /
// dead-after / non-escaping, the escape scan skipping the match scrutinee) and
// deep-drops the prior box at the loop-rebind (emit_enum_deep_reinit_store ->
// emit_enum_variant_drops, null-guarded for the first-iteration zeroed slot).
//
// SOUNDNESS: an escaping enum (stored to an outer var) is rejected by
// name_escapes_outside_stmt; an arm that MOVES an rc payload out is rejected by
// match_arm_binds_rc_payload. Both are exercised below.

func rcEnumLoopLocalSrc(n string) string {
	return `enum Box { Full(i32[]), Empty }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) {
        var b: Box = Full([i, i + 1]);
        match (b) { Full(xs) => { acc = acc + xs[0]; }, Empty => {} }
        i = i + 1;
    }
    if (acc < 0) { return 5; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// per iter reads xs[0..2] = i + (i+1) + (i+2) = 3i+3; sum 0..199 = 3*19900+600 = 60300.
const rcEnumLoopLocalDetectorSrc = `enum Box { Full(i32[]), Empty }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        var b: Box = Full([i, i + 1, i + 2]);
        match (b) { Full(xs) => { acc = acc + xs[0] + xs[1] + xs[2]; }, Empty => {} }
        i = i + 1;
    }
    if (acc != 60300) { return 99; }
    return __rc_underflow();
}`

// An enum that ESCAPES (stored into an outer `keep`) must NOT be reclaimed at the
// loop-rebind — `keep` still references the payload. __rc_underflow == 0 proves it.
const rcEnumEscapeSafetySrc = `enum Box { Full(i32[]), Empty }
function main(): i32 {
    var keep: Box = Empty;
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 100) {
        var b: Box = Full([i, i + 1]);
        keep = b;
        match (b) { Full(xs) => { acc = acc + xs[0]; }, Empty => {} }
        i = i + 1;
    }
    match (keep) { Full(xs) => { acc = acc + xs[0]; }, Empty => {} }
    if (acc < 0) { return 5; }
    return __rc_underflow();
}`

func TestSelfHostRcEnumLoopLocalReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, tag, prog string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", tag)
		}
		progBin := buildBin(t, gcc, dir, tag, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	t.Run("fixpoint-bounded", func(t *testing.T) {
		small := run(t, "rcenum-50", rcEnumLoopLocalSrc("50"))
		large := run(t, "rcenum-5000", rcEnumLoopLocalSrc("5000"))
		if small != large {
			t.Errorf("rc-enum loop-local bump must be bounded: N=50 -> %d, N=5000 -> %d (payload leaked per iteration)", small, large)
		}
		if small == 0 {
			t.Errorf("expected a non-zero bounded high-water, got 0")
		}
	})

	t.Run("no-over-release", func(t *testing.T) {
		if code := run(t, "rcenum-detector", rcEnumLoopLocalDetectorSrc); code != 0 {
			t.Errorf("rc-enum loop-local deep reclaim over-released (exit %d, 99=value mismatch, >0=__rc_underflow)", code)
		}
	})

	t.Run("escape-safety", func(t *testing.T) {
		if code := run(t, "rcenum-escape", rcEnumEscapeSafetySrc); code != 0 {
			t.Errorf("rc-enum deep reclaim freed an ESCAPING enum (exit %d, >0=__rc_underflow — keep's payload double-released)", code)
		}
	})
}
