package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
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
//   - Flag off, no sanitizer symbol is emitted at all (the cheap proxy
//     for the byte-identical release guarantee).

// emitSanitize compiles src with the sanitizer toggled per `on`,
// returning the asm text. Mirrors emitLeakCheck (leakcheck_test.go) but
// drives the flag through ast.ApplySanitize, so the test exercises the
// same fold-down the CLI's -sanitize uses rather than setting the
// component flags by hand.
func emitSanitize(t *testing.T, backend, src string, on bool) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}

	prevFree, prevSan := ast.RcFreeEnabled, ast.SanitizeEnabled
	prevLc, prevTrap, prevDbg := ast.LeakCheckEnabled, ast.RcUnderflowTrap, ast.RcFreeDebug
	restore := func() {
		ast.RcFreeEnabled, ast.SanitizeEnabled = prevFree, prevSan
		ast.LeakCheckEnabled, ast.RcUnderflowTrap, ast.RcFreeDebug = prevLc, prevTrap, prevDbg
	}
	t.Cleanup(restore)
	ast.RcFreeEnabled = true
	ast.SanitizeEnabled = on
	// ApplySanitize only ever turns flags ON, so an ambient FERN_*
	// setting in the developer's environment would leak into the
	// flag-OFF leg and make it lie. Clear them first.
	ast.LeakCheckEnabled, ast.RcUnderflowTrap, ast.RcFreeDebug = false, false, false
	ast.ApplySanitize()

	var asm string
	var emitErr error
	if backend == "arm64-linux" {
		asm, emitErr = arm64codegen.Emit(prog, info)
	} else {
		asm, emitErr = x86_64.Emit(prog, info)
	}
	restore()
	if emitErr != nil {
		t.Fatalf("%s emit: %v", backend, emitErr)
	}
	return asm
}

// runSanitizeX86_64 compiles src with the sanitizer on and runs it,
// returning stdout, stderr and the exit code separately (the report
// contract is "stderr only, stdout untouched").
func runSanitizeX86_64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	return buildAndRunSanitized(t, gcc, runner, emitSanitize(t, "x86_64", src, true), false)
}

// runSanitizeArm64 is the arm64 sibling (qemu; SKIPs without the
// aarch64 toolchain — rides CI).
func runSanitizeArm64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	gcc, qemu := arm64Tooling(t)
	return buildAndRunSanitized(t, gcc, []string{qemu}, emitSanitize(t, "arm64-linux", src, true), true)
}

func buildAndRunSanitized(t *testing.T, gcc string, runner []string, asm string, arm64 bool) (string, string, int) {
	t.Helper()
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	args := []string{"-static", "-nostdlib", asmPath, "-o", binPath}
	if !arm64 {
		args = append([]string{"-no-pie"}, args...)
	}
	if out, err := exec.Command(gcc, args...).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	if arm64 {
		return runSplit(t, runArm64Bin(runner[0], binPath))
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
	}
	return runSplit(t, cmd)
}

// sanCleanSrc is the rc-driven drop-everything loop: every row is
// precisely dropped, so a sanitizer run must be silent.
const sanCleanSrc = `function main(): i32 {
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

// sanLeakSrc: three 60-byte allocations (rounded to 64), one freed,
// exit code 42. The verdict must name 128 bytes in 2 blocks and must
// not clobber main's exit code.
const sanLeakSrc = `function main(): i32 {
    var a: usize = __alloc(60);
    var b: usize = __alloc(60);
    var c: usize = __alloc(60);
    __free(a, 60);
    if (b == c) { return 9; }
    return 42;
}`

// sanDoubleFreeSrc over-releases deliberately: __alloc_u8 hands back an
// rc==1 buffer, the first __rc_dec takes it to 0, and the second sees a
// non-positive count — the exact shape __rc_underflow_count() was built
// to notice. Under the sanitizer that stops being a counter nobody
// reads and becomes a fatal, named report.
const sanDoubleFreeSrc = `function main(): i32 {
    var a: u8[] = __alloc_u8(16);
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
    var i: i32 = 0;
    while (i < 200) {
        var row: i32[] = [i, i + 1, i + 2];
        i = i + row[0] * 0 + 1;
    }
    var b: i64 = __heap_bump_bytes();
    return (b / 1024) as i32;
}`

// sanRcHelpersSrc calls both rc builtins so __fern_rc_inc and
// __fern_rc_dec are both emitted — they carry the use-after-free poison
// check, and a program that touches neither doesn't get either helper.
const sanRcHelpersSrc = `function main(): i32 {
    var a: u8[] = __alloc_u8(16);
    __rc_inc(a);
    __rc_dec(a);
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

// Flag off, the emitted asm must carry no sanitizer symbol at all —
// message, label, or verdict text. The cheap proxy for "a release build
// is byte-identical to one from a compiler without the feature".
func TestSanitizeOffEmitsNoSymbols(t *testing.T) {
	needles := []string{"fern-sanitizer", "__fern_msg_san_", ".Lsan_"}
	for _, backend := range []string{"x86_64", "arm64-linux"} {
		asm := emitSanitize(t, backend, sanCleanSrc, false)
		for _, n := range needles {
			if strings.Contains(asm, n) {
				t.Errorf("%s: flag-off asm contains sanitizer symbol %q", backend, n)
			}
		}
	}
}

// The use-after-free detector is the one check with no deterministic
// Fern-level trigger: producing a real dangling reference means
// producing a compiler bug, and on this runtime the over-release guard
// in __fern_rc_dec sits AHEAD of the free, so a miscount is reported as
// a double free before it can become a stale pointer. (Two hand-built
// miscount shapes were tried; both landed on the over-release path,
// which is the better diagnosis anyway.) So pin the wiring instead:
// under the sanitizer both rc helpers compare the rc word against
// RcPoison and route a match to the named diagnostic through
// __fern_report, and no bare trap is left as a silent death anywhere.
func TestSanitizeWiresUseAfterFreeReport(t *testing.T) {
	for _, tc := range []struct {
		backend string
		// poison is how RcPoison reaches a comparison on this
		// backend: an immediate operand on x86-64, a movz/movk pair
		// into a scratch register on arm64 (the value needs two
		// halves, so there is no single-instruction form).
		poison []string
		// silentTraps are the die-without-a-message instructions this
		// backend used to reach for; none may survive.
		silentTraps []string
	}{
		{
			backend:     "x86_64",
			poison:      []string{fmt.Sprintf("cmp ecx, %d", ast.RcPoison)},
			silentTraps: []string{"ud2"},
		},
		{
			backend: "arm64-linux",
			poison: []string{
				fmt.Sprintf("movz w2, #%d", ast.RcPoison&0xffff),
				fmt.Sprintf("movk w2, #%d, lsl #16", (ast.RcPoison>>16)&0xffff),
			},
			silentTraps: []string{"udf", "brk "},
		},
	} {
		t.Run(tc.backend, func(t *testing.T) {
			asm := emitSanitize(t, tc.backend, sanRcHelpersSrc, true)
			for _, p := range tc.poison {
				if !strings.Contains(asm, p) {
					t.Errorf("no RcPoison check materialised (%q missing)", p)
				}
			}
			for _, want := range []string{"__fern_msg_san_uaf", "fern-sanitizer: use-after-free"} {
				if !strings.Contains(asm, want) {
					t.Errorf("sanitized asm missing %q", want)
				}
			}
			// Every poison match must reach the reporter. Both
			// backends spell the tail jump differently, so assert on
			// the shared destination.
			if !strings.Contains(asm, "__fern_report") {
				t.Error("the use-after-free check does not route to __fern_report")
			}
			for _, trap := range tc.silentTraps {
				if strings.Contains(asm, trap) {
					t.Errorf("sanitized asm still contains a bare %q: a check that dies without a message is what this mode replaces", trap)
				}
			}
		})
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
	// The census stays honest with the quarantine on: a quarantined
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
	if code != x86_64.ExitSanitizer {
		t.Errorf("exit=%d, want %d (a sanitizer finding is fatal and has its own status)", code, x86_64.ExitSanitizer)
	}
	if !strings.Contains(stderr, "fern-sanitizer: rc over-release (double free)") {
		t.Errorf("stderr does not name the finding: %q", stderr)
	}
	if !strings.Contains(stderr, "backtrace:") {
		t.Errorf("stderr carries no #5538 backtrace: %q", stderr)
	}
}

// Without the sanitizer the same over-release only bumps a counter and
// the program runs to completion — the "test-only oracle" state #5545
// set out to promote. Pins that the promotion is opt-in, not a change
// to the default build.
func TestX86_64DoubleFreeSilentWithoutSanitize(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	asm := emitSanitize(t, "x86_64", sanDoubleFreeSrc, false)
	_, stderr, code := buildAndRunSanitized(t, gcc, runner, asm, false)
	if code != 0 {
		t.Errorf("exit=%d, want 0 (an unsanitized build must not abort)", code)
	}
	if stderr != "" {
		t.Errorf("stderr=%q, want empty (an unsanitized build must not report)", stderr)
	}
}

func TestX86_64SanitizeQuarantinesFreedBlocks(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	_, sanErr, sanKiB := buildAndRunSanitized(t, gcc, runner, emitSanitize(t, "x86_64", sanQuarantineSrc, true), false)
	_, _, plainKiB := buildAndRunSanitized(t, gcc, runner, emitSanitize(t, "x86_64", sanQuarantineSrc, false), false)

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
	allocs, frees, live := parseLeakCheckLine(t, strings.TrimPrefix(sanErr, ""))
	if allocs != frees || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want balanced / 0", allocs, frees, live)
	}
}

// --- arm64 legs (qemu; ride CI) ------------------------------------
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
	gcc, qemu := arm64Tooling(t)
	_, sanErr, sanKiB := buildAndRunSanitized(t, gcc, []string{qemu}, emitSanitize(t, "arm64-linux", sanQuarantineSrc, true), true)
	_, _, plainKiB := buildAndRunSanitized(t, gcc, []string{qemu}, emitSanitize(t, "arm64-linux", sanQuarantineSrc, false), true)

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
	if code != arm64codegen.ExitSanitizer {
		t.Errorf("exit=%d, want %d", code, arm64codegen.ExitSanitizer)
	}
	if !strings.Contains(stderr, "fern-sanitizer: rc over-release (double free)") {
		t.Errorf("stderr does not name the finding: %q", stderr)
	}
}
