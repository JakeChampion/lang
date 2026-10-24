package e2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func runSanitizeX86_64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	return runSanitizeX86_64With(t, src)
}

const uncertified = "FERN_RC_CERTIFY=0"

func runSanitizeX86_64With(t *testing.T, src string, extra ...string) (string, string, int) {
	t.Helper()
	runner := e2eharness.X86_64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, src, append([]string{"FERN_SANITIZE=1"}, extra...))
	return runSplit(t, runX86_64Bin(runner, bin))
}

func runSanitizeArm64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	return runSanitizeArm64With(t, src)
}

func runSanitizeArm64With(t *testing.T, src string, extra ...string) (string, string, int) {
	t.Helper()
	qemu := e2eharness.Arm64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Linux, src, append([]string{"FERN_SANITIZE=1"}, extra...))
	return runSplit(t, runArm64Bin(qemu, bin))
}

func runPlain(t *testing.T, target, src string) (string, string, int) {
	t.Helper()
	return runPlainWith(t, target, src)
}

func runPlainWith(t *testing.T, target, src string, extra ...string) (string, string, int) {
	t.Helper()
	if target == e2eharness.TargetArm64Linux {
		qemu := e2eharness.Arm64Runner(t)
		return runSplit(t, runArm64Bin(qemu, e2eharness.CompileSelfHostSource(t, target, src, extra)))
	}
	runner := e2eharness.X86_64Runner(t)
	return runSplit(t, runX86_64Bin(runner, e2eharness.CompileSelfHostSource(t, target, src, extra)))
}

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

const sanLeakSrc = `function main(): i32 {
    let a: usize = __alloc(60);
    let b: usize = __alloc(60);
    let c: usize = __alloc(60);
    __free(a, 60);
    if (b == c) { return 9; }
    return 42;
}`

const sanDoubleFreeSrc = `function main(): i32 {
    let a: u8[] = __alloc_u8(16);
    __rc_dec(a);
    __rc_dec(a);
    return 0;
}`

const sanQuarantineSrc = `function main(): i32 {
    let i: i32 = 0;
    while (i < 200) {
        let row: i32[] = [i, i + 1, i + 2];
        i = i + row[0] * 0 + 1;
    }
    let b: i64 = __heap_bump_bytes();
    return (b / 1024) as i32;
}`

const sanStaleTouchSrc = `function main(): i32 {
    let a: u8[] = __alloc_u8(16);
    let p: usize = a as usize;
    __rc_dec(a);
    __fern_rc_inc(p);
    return 0;
}`

var sanLeakVerdictRe = regexp.MustCompile(`fern-sanitizer: leak (\d+) bytes in (\d+) blocks\n`)

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
	_, stderr, code := runSanitizeX86_64With(t, sanDoubleFreeSrc, uncertified)
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
	_, stderr, code := runSanitizeArm64With(t, sanDoubleFreeSrc, uncertified)
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
