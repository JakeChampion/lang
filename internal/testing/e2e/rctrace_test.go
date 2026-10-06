package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// --- Running a program under the heap tracer (#6068) ------------------------
//
// FERN_RC_TRACE=1 makes the self-host's x86-64 runtime write one stderr line
// per alloc and free:
//
//	rctrace <a|f> <ptr> <size> <site> <caller>
//
// four fixed-width 16-hex-digit numbers. Pairing the `a` lines against the
// `f` lines by pointer leaves the blocks the program never gave back. The
// tracer's own properties (pairing, per-call-site sites, the caller frame,
// agreement with FERN_LEAKCHECK, a flag-off build carrying nothing) are pinned
// in internal/testing/e2ecompiler/self_host_rctrace_test.go; the helpers here serve
// the leak censuses in this package.

var rcTraceLineRe = regexp.MustCompile(`^rctrace ([af]) ([0-9a-f]{16}) ([0-9a-f]{16}) ([0-9a-f]{16}) ([0-9a-f]{16})$`)

// buildTracedX86_64 compiles entry for x86-64 with the tracer on, writing the
// binary into dir. A failure is returned rather than reported, so a census
// worker can record the program as unmeasured.
func buildTracedX86_64(t *testing.T, entry, dir string) (string, error) {
	bin := filepath.Join(dir, "prog")
	cmd := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetX86_64Linux, entry, bin)
	cmd.Env = e2eharness.ChildEnv("FERN_RC_TRACE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("self-host compile: %v\n%s", err, out)
	}
	return bin, nil
}

// runTracedX86_64 compiles src with the tracer on and runs it, returning
// stdout, stderr and the exit code.
func runTracedX86_64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	_, runner := x86_64Tooling(t)
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	bin, err := buildTracedX86_64(t, entry, dir)
	if err != nil {
		t.Fatal(err)
	}
	return runSplit(t, runX86_64Bin(runner, bin))
}

// unpairedAllocs runs src under the tracer and counts the allocations that
// never got a matching free.
func unpairedAllocs(t *testing.T, src string) int {
	t.Helper()
	_, stderr, exit := runTracedX86_64(t, src)
	if exit == -1 {
		t.Fatal("the program was killed by a signal")
	}
	n, _, err := pairRcTrace(stderr)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// The trace belongs on stderr: a traced program prints what it prints and
// exits with main's result.
func TestRcTraceX86_64LeavesStdoutAndExit(t *testing.T) {
	const src = `function main(): i32 {
    let i: i32 = 0;
    while (i < 40) {
        let a: usize = __alloc(64);
        __free(a, 64);
        i = i + 1;
    }
    print("done");
    return 7;
}`
	stdout, stderr, code := runTracedX86_64(t, src)
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
	if stdout != "done\n" {
		t.Errorf("stdout = %q, want %q", stdout, "done\n")
	}
	n, _, err := pairRcTrace(stderr)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d unpaired allocation(s) in a program that frees every block", n)
	}
	if first, _, _ := strings.Cut(stderr, "\n"); !rcTraceLineRe.MatchString(first) {
		t.Errorf("stderr does not open with an rctrace line: %q", first)
	}
}
