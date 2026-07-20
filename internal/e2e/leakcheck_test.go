package e2e

import (
	"bytes"
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

// --- Leak detector, slice 1 (#5362) -------------------------------
//
// ast.LeakCheckEnabled (FERN_LEAKCHECK=1) counts every __fern_alloc
// and __fern_free — count + (size+15)&-16-rounded bytes each, the same
// rounding on both sides so a block's alloc and free cancel exactly —
// and prints one line to stderr at BOTH exit seams (the _start
// epilogue and the exit() builtin's __fern_exit):
//
//	leakcheck: allocs=<N> frees=<M> live_bytes=<K>
//
// __fern_alloc_reuse's in-place path counts as neither an alloc nor a
// free. These tests pin the report's numbers on deterministic
// __alloc/__free shapes and on an rc-driven drop-everything loop (the
// heap-bump flat-heap shape, which rc_heap_bump_test.go already proves
// frees every iteration's buffer), pin exit-code and stdout
// preservation, and pin that a flag-off build emits no __fern_lc_
// symbol at all (the byte-identical guarantee's cheap proxy).

// emitLeakCheck compiles src with ast.LeakCheckEnabled (and the
// freelist) toggled per the flags, returning the asm text. Follows the
// compileX86_64FreeOn pipeline; monomorph is included so the arm64 leg
// can share it.
func emitLeakCheck(t *testing.T, backend, src string, leakCheck bool) string {
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
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	prevFree, prevLc := ast.RcFreeEnabled, ast.LeakCheckEnabled
	t.Cleanup(func() { ast.RcFreeEnabled, ast.LeakCheckEnabled = prevFree, prevLc })
	ast.RcFreeEnabled = true
	ast.LeakCheckEnabled = leakCheck
	var asm string
	var emitErr error
	if backend == "arm64" {
		asm, emitErr = arm64codegen.Emit(prog, info)
	} else {
		asm, emitErr = x86_64.Emit(prog, info)
	}
	ast.RcFreeEnabled, ast.LeakCheckEnabled = prevFree, prevLc
	if emitErr != nil {
		t.Fatalf("%s emit: %v", backend, emitErr)
	}
	return asm
}

// runLeakCheckX86_64 compiles src flag-on and runs it, returning
// stdout, stderr, and the exit code separately (the report contract is
// "stderr only, stdout untouched", so combined output won't do).
func runLeakCheckX86_64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	asm := emitLeakCheck(t, "x86_64", src, true)
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
	}
	return runSplit(t, cmd)
}

// runLeakCheckArm64 is the arm64 sibling (qemu; SKIPs without the
// aarch64 toolchain — rides CI).
func runLeakCheckArm64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	gcc, qemu := arm64Tooling(t)
	asm := emitLeakCheck(t, "arm64", src, true)
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	return runSplit(t, runArm64Bin(qemu, binPath))
}

func runSplit(t *testing.T, cmd *exec.Cmd) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

var leakCheckLineRe = regexp.MustCompile(`^leakcheck: allocs=(-?\d+) frees=(-?\d+) live_bytes=(-?\d+)\n$`)

// parseLeakCheckLine asserts stderr is exactly one well-formed report
// line and returns its three numbers.
func parseLeakCheckLine(t *testing.T, stderr string) (allocs, frees, live int64) {
	t.Helper()
	m := leakCheckLineRe.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("stderr is not a single leakcheck report line: %q", stderr)
	}
	allocs, _ = strconv.ParseInt(m[1], 10, 64)
	frees, _ = strconv.ParseInt(m[2], 10, 64)
	live, _ = strconv.ParseInt(m[3], 10, 64)
	return allocs, frees, live
}

// leakCheckBalancedSrc: 100 paired __alloc/__free of one class. Fully
// deterministic: allocs=100, frees=100, live_bytes=0.
const leakCheckBalancedSrc = `function main(): i32 {
    var i: i32 = 0;
    while (i < 100) {
        var a: usize = __alloc(64);
        __free(a, 64);
        i = i + 1;
    }
    return 0;
}`

// leakCheckRcDropSrc is the rc-driven drop-everything loop — the
// heap-bump flat-heap shape (rc_heap_bump_test.go proves each
// iteration's row buffer is freed and reused, keeping the bump mark
// flat). Every alloc is precisely dropped, so the report must balance
// exactly: allocs == frees, live_bytes == 0. (Shapes that do NOT free
// under phase-1 precise drop — e.g. shared rc>1 buffers whose final dec
// isn't a free site — would show as live; this fixture deliberately has
// no sharing.)
const leakCheckRcDropSrc = `function main(): i32 {
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

// leakCheckLeakySrc: three 60-byte allocations (rounded to 64), one
// freed, exit code 42 through the _start epilogue. Pins the leak
// numbers AND that the report doesn't clobber main's exit code:
// allocs=3, frees=1, live_bytes=2*64=128.
const leakCheckLeakySrc = `function main(): i32 {
    var a: usize = __alloc(60);
    var b: usize = __alloc(60);
    var c: usize = __alloc(60);
    __free(a, 60);
    if (b == c) { return 9; }
    return 42;
}`

// leakCheckExitBuiltinSrc: the exit() builtin bypasses the _start
// epilogue, so __fern_exit must report too — with the exit code (7)
// preserved and stdout (the print) clean of the report. alloc(100)
// rounds to 112 live bytes.
const leakCheckExitBuiltinSrc = `function main(): i32 {
    var a: usize = __alloc(100);
    print("hello");
    exit(7);
    return 0;
}`

// Flag-off, the emitted asm must not contain any leak-detector symbol
// at all — instrumentation, counters, report, and literals are all
// behind the flag (the flag-off byte-identical guarantee).
func TestLeakCheckOffEmitsNoSymbols(t *testing.T) {
	for _, backend := range []string{"x86_64", "arm64"} {
		asm := emitLeakCheck(t, backend, leakCheckLeakySrc, false)
		if strings.Contains(asm, "__fern_lc_") || strings.Contains(asm, ".Llc_") {
			t.Errorf("%s: flag-off asm contains leak-detector symbols", backend)
		}
	}
}

func TestX86_64LeakCheckBalanced(t *testing.T) {
	stdout, stderr, code := runLeakCheckX86_64(t, leakCheckBalancedSrc)
	if code != 0 || stdout != "" {
		t.Fatalf("exit=%d stdout=%q, want 0 / empty", code, stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 100 || frees != 100 || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 100/100/0", allocs, frees, live)
	}
}

func TestX86_64LeakCheckRcDropBalanced(t *testing.T) {
	stdout, stderr, code := runLeakCheckX86_64(t, leakCheckRcDropSrc)
	if code != 0 || stdout != "" {
		t.Fatalf("exit=%d stdout=%q, want 0 / empty", code, stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Errorf("expected a non-zero alloc count (one row per iteration)")
	}
	if allocs != frees || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want balanced / 0 (precise drop frees every row)", allocs, frees, live)
	}
}

func TestX86_64LeakCheckLeakReported(t *testing.T) {
	stdout, stderr, code := runLeakCheckX86_64(t, leakCheckLeakySrc)
	if code != 42 {
		t.Errorf("exit=%d, want 42 (report must not clobber main's exit code)", code)
	}
	if stdout != "" {
		t.Errorf("stdout=%q, want empty (report goes to stderr only)", stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 3 || frees != 1 || live != 128 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 3/1/128", allocs, frees, live)
	}
}

func TestX86_64LeakCheckExitBuiltinReports(t *testing.T) {
	stdout, stderr, code := runLeakCheckX86_64(t, leakCheckExitBuiltinSrc)
	if code != 7 {
		t.Errorf("exit=%d, want 7 (__fern_exit must preserve the code around the report)", code)
	}
	if stdout != "hello\n" {
		t.Errorf("stdout=%q, want %q", stdout, "hello\n")
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 1 || frees != 0 || live != 112 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 1/0/112", allocs, frees, live)
	}
}

// Arm64 mirrors (qemu; ride CI, SKIP without the toolchain).
func TestArm64LeakCheckBalanced(t *testing.T) {
	stdout, stderr, code := runLeakCheckArm64(t, leakCheckBalancedSrc)
	if code != 0 || stdout != "" {
		t.Fatalf("exit=%d stdout=%q, want 0 / empty", code, stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 100 || frees != 100 || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 100/100/0", allocs, frees, live)
	}
}

func TestArm64LeakCheckLeakReported(t *testing.T) {
	stdout, stderr, code := runLeakCheckArm64(t, leakCheckLeakySrc)
	if code != 42 {
		t.Errorf("exit=%d, want 42 (report must not clobber main's exit code)", code)
	}
	if stdout != "" {
		t.Errorf("stdout=%q, want empty (report goes to stderr only)", stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 3 || frees != 1 || live != 128 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 3/1/128", allocs, frees, live)
	}
}

func TestArm64LeakCheckExitBuiltinReports(t *testing.T) {
	stdout, stderr, code := runLeakCheckArm64(t, leakCheckExitBuiltinSrc)
	if code != 7 {
		t.Errorf("exit=%d, want 7 (__fern_exit must preserve the code around the report)", code)
	}
	if stdout != "hello\n" {
		t.Errorf("stdout=%q, want %q", stdout, "hello\n")
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 1 || frees != 0 || live != 112 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 1/0/112", allocs, frees, live)
	}
}
