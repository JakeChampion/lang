package e2ecompiler

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const sanSelfHostCleanSrc = `function main(): i32 {
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

const sanSelfHostLeakSrc = `function main(): i32 {
    let a: usize = __alloc(60);
    let b: usize = __alloc(60);
    let c: usize = __alloc(60);
    __free(a, 60);
    if (b == c) { return 9; }
    return 42;
}`

const sanSelfHostDoubleFreeSrc = `function main(): i32 {
    let a: u8[] = __alloc_u8(16);
    __rc_dec(a);
    __rc_dec(a);
    return 0;
}`

var sanSelfHostLeakRe = regexp.MustCompile(`fern-sanitizer: leak (\d+) bytes in (\d+) blocks\n`)

func sanSelfHostBuild(t *testing.T, name, src string, env []string) (string, []string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
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
	if blocks != 2 {
		t.Errorf("verdict says %d blocks, want 2 (the same count native's "+
			"TestX86_64SanitizeLeakVerdict reads for this program)", blocks)
	}
	if bytes < 2*60 || bytes%blocks != 0 {
		t.Errorf("verdict says %d bytes across %d blocks, want a consistent per-block size >= 60", bytes, blocks)
	}
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
	bin, runner := sanSelfHostBuild(t, "san_dfree", sanSelfHostDoubleFreeSrc, []string{"FERN_SANITIZE=1", uncertified})
	stderr, code := hevRun(t, runner, bin)
	if code != e2eharness.ExitSanitizer {
		t.Errorf("exit=%d, want %d (a sanitizer finding is fatal and has its own status)", code, e2eharness.ExitSanitizer)
	}
	if !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)\n") {
		t.Errorf("stderr does not carry the diagnostic: %q", stderr)
	}
}

func TestSelfHostDoubleFreeSilentWithoutSanitizeX86_64(t *testing.T) {
	bin, runner := sanSelfHostBuild(t, "san_dfree_off", sanSelfHostDoubleFreeSrc, []string{uncertified})
	stderr, code := hevRun(t, runner, bin)
	if code != 0 {
		t.Errorf("exit=%d, want 0 (an unsanitized build must not abort)", code)
	}
	if stderr != "" {
		t.Errorf("stderr=%q, want empty (an unsanitized build must not report)", stderr)
	}
}

func TestSelfHostSanitizeOffEmitsNoSymbolsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
	if strings.Contains(on, "rctrace") {
		t.Error("FERN_SANITIZE=1 emitted the rctrace hook; it is a targeted probe, not part of the mode")
	}
}

const sanMapDoubleDropSrc = `import "core/map";
function main(): i32 {
    let h: usize = map_new_impl(4, 0, 0);
    __map_drop_impl(h);
    __map_drop_impl(h);
    return 7;
}`

const sanMapOverReleaseSrc = `import "core/map";
function main(): i32 {
    let h: usize = map_new_impl(4, 0, 0);
    __store_i32(h - 8, 0);
    __map_drop_impl(h);
    return __rc_underflow_count();
}`

const uncertified = "FERN_RC_CERTIFY=0"

func TestSelfHostSanitizeBoxFreeIsQuarantined(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cases := []struct{ name, src, finding string }{
		{"double-drop", sanMapDoubleDropSrc, "fern-sanitizer: use-after-free (touched a quarantined block)"},
		{"over-release", sanMapOverReleaseSrc, "fern-sanitizer: rc over-release (double free)"},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, tc := range cases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				stderr, code := cli.exitOf(t, tc.src, target, "FERN_SANITIZE=1", uncertified)
				if code != e2eharness.ExitSanitizer || !strings.Contains(stderr, tc.finding) {
					t.Errorf("exit=%d stderr=%q, want exit %d naming %q", code, stderr, e2eharness.ExitSanitizer, tc.finding)
				}
			})
		}
	}
}

var sanBacktraceRe = regexp.MustCompile(`backtrace:\n(  0x[0-9a-f]{16}\n)+`)

func TestSelfHostSanitizeReportCarriesBacktrace(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cases := []struct{ name, src, finding string }{
		{"use-after-free", sanMapDoubleDropSrc, "fern-sanitizer: use-after-free (touched a quarantined block)\n"},
		{"over-release", sanMapOverReleaseSrc, "fern-sanitizer: rc over-release (double free)\n"},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, tc := range cases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				stderr, code := cli.exitOf(t, tc.src, target, "FERN_SANITIZE=1", uncertified)
				if code != e2eharness.ExitSanitizer || !strings.Contains(stderr, tc.finding) {
					t.Fatalf("exit=%d stderr=%q, want exit %d naming %q", code, stderr, e2eharness.ExitSanitizer, tc.finding)
				}
				if !sanBacktraceRe.MatchString(stderr) {
					t.Errorf("no backtrace under the report: %q", stderr)
				}
			})
			t.Run(target+"/"+tc.name+"/FERN_BACKTRACE=0", func(t *testing.T) {
				stderr, code := cli.exitOf(t, tc.src, target, "FERN_SANITIZE=1", "FERN_BACKTRACE=0", uncertified)
				if code != e2eharness.ExitSanitizer || !strings.Contains(stderr, tc.finding) {
					t.Fatalf("exit=%d stderr=%q, want exit %d naming %q", code, stderr, e2eharness.ExitSanitizer, tc.finding)
				}
				if strings.Contains(stderr, "backtrace:") {
					t.Errorf("FERN_BACKTRACE=0 still walked: %q", stderr)
				}
			})
		}
	}
}
