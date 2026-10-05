package e2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/e2eharness"
)

// --- Sanitizer mode (#5545) ---------------------------------------
//
// ast.SanitizeEnabled (FERN_SANITIZE=1, or the CLI's -sanitize) is the
// single opt-in surface over the heap memory-safety detectors that used
// to be three separately-named env vars nobody could be expected to
// know: the leak census, the rc over-release (double-free) detector,
// and the use-after-free quarantine.
//
// What the mode promises, and what these tests pin:
//
//   - A clean program is SILENT of `fern-sanitizer:` lines, and its
//     exit code and stdout are untouched. "Was this run clean" is
//     answerable without reading a number.
//   - A leak gets a verdict line after the leakcheck summary.
//   - An rc over-release is REPORTED (named message + #5538 backtrace)
//     and fatal with ExitSanitizer, not a silent counter bump.
//   - A freed block is quarantined — never recycled — while STILL
//     counting as a free for the census. That combination is the whole
//     reason the two detectors can be on at once; account at the
//     release and every correctly-freed array would otherwise read as
//     a leak.
//
// Flag off, no sanitizer symbol is emitted at all and a defect runs
// silently; internal/e2eselfhost pins both (TestSelfHostSanitizeOff*,
// the *AsmContract* and *SilentWithout* tests).

// runSanitizeX86_64 compiles src with the sanitizer on and runs it,
// returning stdout, stderr and the exit code separately (the report
// contract is "stderr only, stdout untouched").
func runSanitizeX86_64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	runner := e2eharness.X86_64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, src, []string{"FERN_SANITIZE=1"})
	return runSplit(t, runX86_64Bin(runner, bin))
}

// runSanitizeArm64 is the arm64 sibling (qemu; SKIPs without qemu-aarch64 —
// runs in CI).
func runSanitizeArm64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	qemu := e2eharness.Arm64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Linux, src, []string{"FERN_SANITIZE=1"})
	return runSplit(t, runArm64Bin(qemu, bin))
}

// runPlain compiles src for target with no detector on and runs it on the
// target's runner, splitting stdout, stderr and the exit code.
func runPlain(t *testing.T, target, src string) (string, string, int) {
	t.Helper()
	if target == e2eharness.TargetArm64Linux {
		qemu := e2eharness.Arm64Runner(t)
		return runSplit(t, runArm64Bin(qemu, e2eharness.CompileSelfHostSource(t, target, src, nil)))
	}
	runner := e2eharness.X86_64Runner(t)
	return runSplit(t, runX86_64Bin(runner, e2eharness.CompileSelfHostSource(t, target, src, nil)))
}

// checkSanitizedBalanced runs src under the sanitizer and asserts the answer,
// no finding, and a census that allocated and freed everything.
func checkSanitizedBalanced(t *testing.T, src string, want int, run func(*testing.T, string) (string, string, int)) {
	t.Helper()
	stdout, stderr, code := run(t, src)
	if code != want {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, want, stdout, stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("sanitizer report:\n%s", stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Errorf("census: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

// sanCleanSrc is the rc-driven drop-everything loop: every row is
// precisely dropped, so a sanitizer run must be silent.
const sanCleanSrc = `function main(): i32 {
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < 50) {
        let row: i32[] = [i, i + 1, i + 2];
        sum = sum + row[0];
        i = i + 1;
    }
    if (sum == 1225) { return 0; }
    return 1;
}`

// sanLeakSrc: three 60-byte allocations (rounded to 64), one freed,
// exit code 42. The verdict must name 128 bytes in 2 blocks and must
// not clobber main's exit code.
const sanLeakSrc = `function main(): i32 {
    let a: usize = __alloc(60);
    let b: usize = __alloc(60);
    let c: usize = __alloc(60);
    __free(a, 60);
    if (b == c) { return 9; }
    return 42;
}`

// sanDoubleFreeSrc releases a buffer twice. __rc_dec is the freeing dec, so
// the first call reclaims the block and poisons its rc word and the second
// touches the poison: a use-after-free report.
const sanDoubleFreeSrc = `function main(): i32 {
    let a: u8[] = __alloc_u8(16);
    __rc_dec(a);
    __rc_dec(a);
    return 0;
}`

// sanQuarantineSrc allocates and drops 200 same-sized rows, then
// reports the bump allocator's high-water mark in KiB. With the
// freelist live the rows all reuse one block and the mark stays near
// zero; under the sanitizer nothing is ever recycled, so the mark grows
// with the round count. That growth IS the quarantine — the property
// the use-after-free detector rests on, since a recycled block would
// overwrite its own poison.
const sanQuarantineSrc = `function main(): i32 {
    let i: i32 = 0;
    while (i < 200) {
        let row: i32[] = [i, i + 1, i + 2];
        i = i + row[0] * 0 + 1;
    }
    let b: i64 = __heap_bump_bytes();
    return (b / 1024) as i32;
}`

// sanStaleTouchSrc builds a real dangling reference: __rc_dec frees the
// buffer and poisons its rc word, and the retained raw address then reaches
// __fern_rc_inc, which reads the poison. Perceus's own drop of `a` at scope
// exit is never reached.
const sanStaleTouchSrc = `function main(): i32 {
    let a: u8[] = __alloc_u8(16);
    let p: usize = a as usize;
    __rc_dec(a);
    __fern_rc_inc(p);
    return 0;
}`

var sanLeakVerdictRe = regexp.MustCompile(`fern-sanitizer: leak (\d+) bytes in (\d+) blocks\n`)

// ApplySanitize is the fold-down every entry point shares: the backends
// read the component flags directly, so a late -sanitize has to push
// into them. It must also compose with an individually-set flag rather
// than replacing the set.
func TestApplySanitizeFoldsIntoComponentFlags(t *testing.T) {
	prevSan := ast.SanitizeEnabled
	prevLc, prevTrap, prevDbg, prevTrace := ast.LeakCheckEnabled, ast.RcUnderflowTrap, ast.RcFreeDebug, ast.RcTrace
	t.Cleanup(func() {
		ast.SanitizeEnabled = prevSan
		ast.LeakCheckEnabled, ast.RcUnderflowTrap, ast.RcFreeDebug, ast.RcTrace = prevLc, prevTrap, prevDbg, prevTrace
	})

	ast.SanitizeEnabled = false
	ast.LeakCheckEnabled, ast.RcUnderflowTrap, ast.RcFreeDebug, ast.RcTrace = false, false, false, false
	ast.ApplySanitize()
	if ast.LeakCheckEnabled || ast.RcUnderflowTrap || ast.RcFreeDebug {
		t.Error("ApplySanitize turned checks on with SanitizeEnabled false")
	}

	ast.SanitizeEnabled = true
	ast.ApplySanitize()
	if !ast.LeakCheckEnabled || !ast.RcUnderflowTrap || !ast.RcFreeDebug {
		t.Errorf("ApplySanitize left a check off: leak=%v trap=%v uaf=%v",
			ast.LeakCheckEnabled, ast.RcUnderflowTrap, ast.RcFreeDebug)
	}
	if ast.RcTrace {
		t.Error("ApplySanitize enabled RcTrace: per-heap-event output is a targeted probe, not a standing mode")
	}
}

func TestX86_64SanitizeCleanRunIsSilent(t *testing.T) {
	stdout, stderr, code := runSanitizeX86_64(t, sanCleanSrc)
	if code != 0 || stdout != "" {
		t.Fatalf("exit=%d stdout=%q, want 0 / empty", code, stdout)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("clean program reported a sanitizer finding: %q", stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Error("expected a non-zero alloc count (one row per iteration)")
	}
	// The census stays correct with the quarantine on: a quarantined
	// block is accounted at its release, so precise drop still
	// balances. Before that fix every freed array read as a leak.
	if allocs != frees || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestX86_64SanitizeLeakVerdict(t *testing.T) {
	stdout, stderr, code := runSanitizeX86_64(t, sanLeakSrc)
	if code != 42 {
		t.Errorf("exit=%d, want 42 (the verdict must not clobber main's exit code)", code)
	}
	if stdout != "" {
		t.Errorf("stdout=%q, want empty (the report goes to stderr only)", stdout)
	}
	m := sanLeakVerdictRe.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no leak verdict line in stderr: %q", stderr)
	}
	bytes, _ := strconv.Atoi(m[1])
	blocks, _ := strconv.Atoi(m[2])
	if bytes != 128 || blocks != 2 {
		t.Errorf("verdict says %d bytes in %d blocks, want 128 / 2", bytes, blocks)
	}
}

func TestX86_64SanitizeDoubleFreeReported(t *testing.T) {
	_, stderr, code := runSanitizeX86_64(t, sanDoubleFreeSrc)
	if code != e2eharness.ExitSanitizer {
		t.Errorf("exit=%d, want %d (a sanitizer finding is fatal and has its own status)", code, e2eharness.ExitSanitizer)
	}
	if !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)") {
		t.Errorf("stderr does not name the finding: %q", stderr)
	}
	if !strings.Contains(stderr, "backtrace:") {
		t.Errorf("stderr carries no #5538 backtrace: %q", stderr)
	}
}

func TestX86_64SanitizeUseAfterFreeReported(t *testing.T) {
	_, stderr, code := runSanitizeX86_64(t, sanStaleTouchSrc)
	if code != e2eharness.ExitSanitizer {
		t.Errorf("exit=%d, want %d", code, e2eharness.ExitSanitizer)
	}
	if !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)") {
		t.Errorf("stderr does not name the finding: %q", stderr)
	}
	if strings.Contains(stderr, "rc over-release") {
		t.Errorf("the stale touch was diagnosed as an over-release, not a use-after-free: %q", stderr)
	}
	if !strings.Contains(stderr, "backtrace:") {
		t.Errorf("stderr carries no #5538 backtrace: %q", stderr)
	}
}

func TestX86_64SanitizeQuarantinesFreedBlocks(t *testing.T) {
	_, sanErr, sanKiB := runSanitizeX86_64(t, sanQuarantineSrc)
	_, _, plainKiB := runPlain(t, e2eharness.TargetX86_64Linux, sanQuarantineSrc)

	// 200 rows × 32 B ≈ 6 KiB if nothing is recycled; near zero if the
	// freelist hands the same block back every round.
	if sanKiB < 4 {
		t.Errorf("sanitized bump high-water = %d KiB, want >= 4 (freed blocks must not be recycled)", sanKiB)
	}
	if plainKiB >= sanKiB {
		t.Errorf("unsanitized bump high-water = %d KiB, sanitized = %d KiB: the default build should still recycle", plainKiB, sanKiB)
	}
	// Quarantined, but still counted: the census must not read the
	// un-recycled blocks as leaks.
	allocs, frees, live := parseLeakCheckLine(t, sanErr)
	if allocs != frees || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want balanced / 0", allocs, frees, live)
	}
}

// --- arm64 legs (qemu; run in CI) ----------------------------------
//
// arm64 carries the whole mode: census, rc over-release report, and the
// use-after-free quarantine. The two backends' diagnostics are the same
// bytes and the same exit status, so a `fern-sanitizer:` line does not
// tell you which native produced it — which is the point, since the
// advice "build it with -sanitize" has to mean one thing.
//
// arm64's quarantine has one site x86-64 does not need: __fern_str_inc
// INLINES its rc bump rather than tail-calling __fern_rc_inc (it has to
// preserve the (data, len) pair in x0/x1), so it carries its own poison
// check. A stale retain of a freed string would otherwise walk straight
// past the detector.

func TestArm64SanitizeCleanRunIsSilent(t *testing.T) {
	stdout, stderr, code := runSanitizeArm64(t, sanCleanSrc)
	if code != 0 || stdout != "" {
		t.Fatalf("exit=%d stdout=%q, want 0 / empty", code, stdout)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("clean program reported a sanitizer finding: %q", stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want non-zero and balanced", allocs, frees, live)
	}
}

func TestArm64SanitizeLeakVerdict(t *testing.T) {
	_, stderr, code := runSanitizeArm64(t, sanLeakSrc)
	if code != 42 {
		t.Errorf("exit=%d, want 42", code)
	}
	m := sanLeakVerdictRe.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no leak verdict line in stderr: %q", stderr)
	}
	bytes, _ := strconv.Atoi(m[1])
	blocks, _ := strconv.Atoi(m[2])
	if bytes != 128 || blocks != 2 {
		t.Errorf("verdict says %d bytes in %d blocks, want 128 / 2 (must match x86-64 exactly)", bytes, blocks)
	}
}

func TestArm64SanitizeQuarantinesFreedBlocks(t *testing.T) {
	_, sanErr, sanKiB := runSanitizeArm64(t, sanQuarantineSrc)
	_, _, plainKiB := runPlain(t, e2eharness.TargetArm64Linux, sanQuarantineSrc)

	if sanKiB < 4 {
		t.Errorf("sanitized bump high-water = %d KiB, want >= 4 (freed blocks must not be recycled)", sanKiB)
	}
	if plainKiB >= sanKiB {
		t.Errorf("unsanitized bump high-water = %d KiB, sanitized = %d KiB: the default build should still recycle", plainKiB, sanKiB)
	}
	allocs, frees, live := parseLeakCheckLine(t, sanErr)
	if allocs != frees || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestArm64SanitizeDoubleFreeReported(t *testing.T) {
	_, stderr, code := runSanitizeArm64(t, sanDoubleFreeSrc)
	if code != e2eharness.ExitSanitizer {
		t.Errorf("exit=%d, want %d", code, e2eharness.ExitSanitizer)
	}
	if !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)") {
		t.Errorf("stderr does not name the finding: %q", stderr)
	}
	if !strings.Contains(stderr, "backtrace:") {
		t.Errorf("stderr carries no #5538 backtrace: %q", stderr)
	}
}

func TestArm64SanitizeUseAfterFreeReported(t *testing.T) {
	_, stderr, code := runSanitizeArm64(t, sanStaleTouchSrc)
	if code != e2eharness.ExitSanitizer {
		t.Errorf("exit=%d, want %d", code, e2eharness.ExitSanitizer)
	}
	if !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)") {
		t.Errorf("stderr does not name the finding: %q", stderr)
	}
	if strings.Contains(stderr, "rc over-release") {
		t.Errorf("the stale touch was diagnosed as an over-release, not a use-after-free: %q", stderr)
	}
	if !strings.Contains(stderr, "backtrace:") {
		t.Errorf("stderr carries no #5538 backtrace: %q", stderr)
	}
}
