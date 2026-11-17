package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const sanOwnerReport = "fern-sanitizer: second context (a thread or stack that does not own the heap used it)"

// sanStraySrc makes a thread with a raw clone on a stack of its own, and
// the thread allocates. Only the runtime floor reaches __syscall6, so the
// runtime CLI compiles it. The spawner sleeps for at most two seconds.
// The syscall numbers are mmap, clone and nanosleep.
func sanStraySrc(mmap, clone, nanosleep int) string {
	r := strings.NewReplacer("MMAP", strconv.Itoa(mmap), "CLONE", strconv.Itoa(clone), "NANOSLEEP", strconv.Itoa(nanosleep))
	return r.Replace(`@noinline
function grow(n: i32): i32 {
  let out: string = "";
  let i: i32 = 0;
  while (i < n) {
    out = out + "ab";
    i = i + 1;
  }
  return out.len();
}

function main(): i32 {
  let ts: usize = __raw_alloc(16);
  __raw_store_ptr(ts, 0, 0 as usize);
  __raw_store_ptr(ts, 8, 10000000 as usize);
  let stack: usize = __syscall6(MMAP, 0, 65536, 3, 34, 0 - 1, 0) as usize;
  let r: i64 = __syscall6(CLONE, 331520, (stack + 65536) as i64, 0, 0, 0, 0);
  if (r == 0) {
    grow(8);
  }
  let i: i32 = 0;
  while (i < 200) {
    __syscall6(NANOSLEEP, ts as i64, 0, 0, 0, 0, 0);
    i = i + 1;
  }
  return grow(3);
}
`)
}

func compileRuntimeFloor(t *testing.T, target, src string, args []string, env ...string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	cmd := exec.Command(e2eharness.RuntimeCLI(t), append(append([]string{"-target", target}, args...), "-o", out, srcPath, e2eharness.SelfHostStdlibRoot(t))...)
	cmd.Env = e2eharness.SelfHostChildEnv(env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("runtime CLI -target %s: %v\n%s", target, err, msg)
	}
	return out
}

func checkSecondContextReport(t *testing.T, stderr string, code int) {
	t.Helper()
	if code != e2eharness.ExitSanitizer {
		t.Errorf("exit=%d, want %d\nstderr: %s", code, e2eharness.ExitSanitizer, stderr)
	}
	if !strings.Contains(stderr, sanOwnerReport) {
		t.Errorf("stderr does not name the finding: %q", stderr)
	}
	if !strings.Contains(stderr, "backtrace:") {
		t.Errorf("stderr carries no backtrace: %q", stderr)
	}
}

// A thread that runs Fern code off the stack of the program owns no heap.
// Its first allocation is a sanitizer finding.
func TestX86_64SanitizeSecondContextReported(t *testing.T) {
	runner := e2eharness.X86_64Runner(t)
	bin := compileRuntimeFloor(t, e2eharness.TargetX86_64Linux, sanStraySrc(9, 56, 35), nil, "FERN_SANITIZE=1")
	_, stderr, code := runSplit(t, runX86_64Bin(runner, bin))
	checkSecondContextReport(t, stderr, code)
}

func TestArm64SanitizeSecondContextReported(t *testing.T) {
	qemu := e2eharness.Arm64Runner(t)
	bin := compileRuntimeFloor(t, e2eharness.TargetArm64Linux, sanStraySrc(222, 220, 101), nil, "FERN_SANITIZE=1")
	_, stderr, code := runSplit(t, runArm64Bin(qemu, bin))
	checkSecondContextReport(t, stderr, code)
}

// The stack check passes for every context of a program that has only one.
func TestSanitizeOwnerCleanRunIsSilent(t *testing.T) {
	for _, run := range []func(*testing.T, string) (string, string, int){runSanitizeX86_64, runSanitizeArm64} {
		checkSanitizedBalanced(t, sanCleanSrc, 0, run)
	}
}
