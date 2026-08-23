package e2eselfhost

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// --- The self-host compiler's sanitizer port (#5545) ---------------
//
// FERN_SANITIZE=1 is read by the self-host compiler at EMIT time (the
// FERN_LEAKCHECK / FERN_RC_TRACE precedent), so the flag goes to the
// driver process, not to the program it produces.
//
// This backend's half of the mode is the leak census, the rc
// over-release report, and the use-after-free quarantine (the
// RcFreeDebug port — self_host_uaf_quarantine_test.go). One deliberate
// gap versus native, an honest subset rather than silently-different
// behaviour: no backtrace under the report (there is no __fern_report
// equivalent here, so the message is the whole diagnostic). Recorded on
// sanitize_on in asm_ir.fern.
//
// What must NOT differ is the text and the exit status: a
// `fern-sanitizer:` line must not tell you which compiler built the
// binary. That is what these tests are mostly for.

// sanExitStatus is the status a sanitizer finding exits with, matching
// x86_64.ExitSanitizer / arm64.ExitSanitizer. Duplicated rather than
// imported because this package tests the SELF-HOST compiler, whose
// copy of the number lives in Fern source — an import would assert the
// Go constant against itself and prove nothing.
const sanExitStatus = 124

// sanSelfHostCleanSrc: the rc-driven drop-everything loop. Every row is
// precisely dropped, so a sanitizer run must be silent.
const sanSelfHostCleanSrc = `function main(): i32 {
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < 50) {
        var row: i32[] = [i, i + 1, i + 2];
        sum = sum + row[0];
        i = i + 1;
    }
    if (sum == 1225) { return 0; }
    return 1;
}`

// sanSelfHostLeakSrc: three raw allocations, none reclaimed. `__free` is
// a documented no-op in this runtime (irlower.fern: "a no-op under the
// bump/leak heap"), so unlike the native leg NOTHING here is freed and
// the census says so — which is the census being accurate about the
// runtime it is measuring, not a divergence to paper over. Exit code 42
// rides through the report untouched.
const sanSelfHostLeakSrc = `function main(): i32 {
    var a: usize = __alloc(60);
    var b: usize = __alloc(60);
    var c: usize = __alloc(60);
    __free(a, 60);
    if (b == c) { return 9; }
    return 42;
}`

// sanSelfHostDoubleFreeSrc over-releases deliberately: __alloc_u8 hands
// back an rc==1 buffer and __rc_dec is dec'd twice. In THIS runtime
// __rc_dec maps to the freeing __fn___fern_arr_dec, so the first dec
// reclaims the block and — under the quarantine the sanitizer implies —
// poisons its rc word; the second dec then touches a quarantined block
// and dies with the use-after-free report. (Native's plain __fern_rc_dec
// never frees, so the same source there leaves rc at 0 and reports the
// over-release text instead — an intrinsic-semantics difference like the
// __free-is-a-no-op one above, not a diagnostic divergence: both texts
// are byte-identical across backends, and each fires for the mechanism
// that actually happened in its runtime.)
const sanSelfHostDoubleFreeSrc = `function main(): i32 {
    var a: u8[] = __alloc_u8(16);
    __rc_dec(a);
    __rc_dec(a);
    return 0;
}`

var sanSelfHostLeakRe = regexp.MustCompile(`fern-sanitizer: leak (\d+) bytes in (\d+) blocks\n`)

// sanSelfHostBuild compiles src through the self-host driver with the
// given emit-time env, links it, and returns the built binary path plus
// the runner needed to execute it.
func sanSelfHostBuild(t *testing.T, name, src string, env []string) (string, []string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	asm := hevCompile(t, runner, driverBin, src, env)
	return buildBin(t, gcc, dir, name, asm), runner
}

func TestSelfHostSanitizeCleanRunIsSilentX86_64(t *testing.T) {
	bin, runner := sanSelfHostBuild(t, "san_clean", sanSelfHostCleanSrc, []string{"FERN_SANITIZE=1"})
	stderr, code := hevRun(t, runner, bin)
	if code != 0 {
		t.Errorf("exit=%d, want 0", code)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("clean program reported a sanitizer finding: %q", stderr)
	}
	// FERN_SANITIZE implies the census (sanitize_on folds into
	// leak_check_on), so the summary must be there even though the
	// verdict is not.
	var allocs, frees, live int64
	if _, err := fmtSscan(stderr, &allocs, &frees, &live); err != nil {
		t.Fatalf("no leakcheck summary under FERN_SANITIZE=1: %v (stderr %q)", err, stderr)
	}
	if allocs == 0 {
		t.Error("expected a non-zero alloc count (one row per iteration)")
	}
	if allocs != frees || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestSelfHostSanitizeLeakVerdictX86_64(t *testing.T) {
	bin, runner := sanSelfHostBuild(t, "san_leak", sanSelfHostLeakSrc, []string{"FERN_SANITIZE=1"})
	stderr, code := hevRun(t, runner, bin)
	if code != 42 {
		t.Errorf("exit=%d, want 42 (the verdict must not clobber main's exit code)", code)
	}
	m := sanSelfHostLeakRe.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no leak verdict line in stderr: %q", stderr)
	}
	bytes, _ := strconv.Atoi(m[1])
	blocks, _ := strconv.Atoi(m[2])
	// Three 60-byte requests, none reclaimed. The exact byte total
	// depends on this runtime's allocation granularity, so assert the
	// block count (which does not) and that the byte figure is a
	// consistent multiple rather than pinning a granularity the
	// allocator is free to change.
	if blocks != 3 {
		t.Errorf("verdict says %d blocks, want 3", blocks)
	}
	if bytes < 3*60 || bytes%blocks != 0 {
		t.Errorf("verdict says %d bytes across %d blocks, want a consistent per-block size >= 60", bytes, blocks)
	}
	// The verdict must agree with the summary it follows.
	var allocs, frees, live int64
	if _, err := fmtSscan(stderr, &allocs, &frees, &live); err != nil {
		t.Fatalf("no leakcheck summary: %v", err)
	}
	if int64(bytes) != live || int64(blocks) != allocs-frees {
		t.Errorf("verdict (%d bytes, %d blocks) disagrees with summary (live=%d, allocs-frees=%d)",
			bytes, blocks, live, allocs-frees)
	}
}

func TestSelfHostSanitizeDoubleFreeReportedX86_64(t *testing.T) {
	bin, runner := sanSelfHostBuild(t, "san_dfree", sanSelfHostDoubleFreeSrc, []string{"FERN_SANITIZE=1"})
	stderr, code := hevRun(t, runner, bin)
	if code != sanExitStatus {
		t.Errorf("exit=%d, want %d (a sanitizer finding is fatal and has its own status)", code, sanExitStatus)
	}
	// Byte-for-byte the native backends' text — this is the assertion
	// that keeps "build it with -sanitize" meaning one thing. The
	// quarantine (the RcFreeDebug port) catches the re-free at the
	// poison it left, one instruction before the underflow test would
	// have seen a zero that no longer exists.
	if !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)\n") {
		t.Errorf("stderr does not carry the diagnostic: %q", stderr)
	}
}

// Without the flag the same over-release only bumps a counter and the
// program runs to completion — the "test-only oracle" state #5545 set
// out to promote. Pins that the promotion is opt-in rather than a
// change to what the self-host compiler emits by default.
func TestSelfHostDoubleFreeSilentWithoutSanitizeX86_64(t *testing.T) {
	bin, runner := sanSelfHostBuild(t, "san_dfree_off", sanSelfHostDoubleFreeSrc, nil)
	stderr, code := hevRun(t, runner, bin)
	if code != 0 {
		t.Errorf("exit=%d, want 0 (an unsanitized build must not abort)", code)
	}
	if stderr != "" {
		t.Errorf("stderr=%q, want empty (an unsanitized build must not report)", stderr)
	}
}

// Flag off, no sanitizer symbol reaches the emitted asm — the cheap
// proxy for "an ordinary build is byte-identical to one from a compiler
// without the mode". The companion half asserts the markers DO appear
// when asked for, so this is testing a gate rather than a typo.
func TestSelfHostSanitizeOffEmitsNoSymbolsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	off := hevCompile(t, runner, driverBin, sanSelfHostCleanSrc, nil)
	for _, marker := range []string{"fern-sanitizer", "__fern_san_abort", ".Lsan_"} {
		if strings.Contains(off, marker) {
			t.Errorf("flag-off asm contains %q — the feature is not fully gated", marker)
		}
	}

	on := hevCompile(t, runner, driverBin, sanSelfHostCleanSrc, []string{"FERN_SANITIZE=1"})
	for _, marker := range []string{"__fern_san_abort", ".Lsan_df", ".Lsan_leak", "__fern_lc_report"} {
		if !strings.Contains(on, marker) {
			t.Errorf("flag-on asm is missing %q", marker)
		}
	}
	// FERN_SANITIZE must not drag in the per-heap-event tracer: that is
	// one stderr line per alloc, which no standing mode can afford.
	if strings.Contains(on, "rctrace") {
		t.Error("FERN_SANITIZE=1 emitted the rctrace hook; it is a targeted probe, not part of the mode")
	}
}
