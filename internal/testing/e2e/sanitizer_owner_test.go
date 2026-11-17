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

// sanUninitSrc reads raw memory that it never wrote. Under -sanitize each
// byte holds the poison pattern 0x7EEDFACE7EEDFACE, little-endian.
const sanUninitSrc = `function main(): i32 {
  unsafe {
    let p: usize = __alloc(20);
    let w: i64 = __load_i64(p + 8 as usize);
    let b: i32 = __load_u8(p + 19 as usize);
    let a: u8[] = __alloc_u8_uninit(13);
    let c: i32 = a[12] as i32;
    __free(p, 20);
    if (w == 9146242183059077838i64 && b == 126 && c == 206) {
      return 42;
    }
    return 1;
  }
}`

// A fresh raw block and the elements of __alloc_u8_uninit hold the poison
// pattern, so a read of memory that the program never wrote gives the same
// value on each run.
func TestSanitizeFillsFreshRawBlocks(t *testing.T) {
	for name, run := range map[string]func(*testing.T, string) (string, string, int){"x86-64": runSanitizeX86_64, "arm64": runSanitizeArm64} {
		t.Run(name, func(t *testing.T) {
			_, stderr, code := run(t, sanUninitSrc)
			if code != 42 || strings.Contains(stderr, "fern-sanitizer:") {
				t.Errorf("exit=%d, want 42 and no finding\nstderr: %s", code, stderr)
			}
		})
	}
}

// sanBoxOverflowSrc writes the byte after the last word of a 16-byte array:
// the first byte of the redzone at the end of its size class. OFFSET is 24 to
// write past the box, or 23 for its last byte.
const sanBoxOverflowSrc = `function main(): i32 {
  unsafe {
    let a: u8[] = __alloc_u8(16);
    let p: usize = a as usize;
    __store_u8(p + OFFSET as usize, 1);
    return a.len();
  }
}`

// A write past the end of a typed box is a finding when the box is released.
// A write inside the box is not.
func TestSanitizeTypedBoxOverflowReported(t *testing.T) {
	const report = "fern-sanitizer: heap-buffer-overflow (a block was written past its end)"
	for name, run := range map[string]func(*testing.T, string, ...string) (string, string, int){"x86-64": runSanitizeX86_64With, "arm64": runSanitizeArm64With} {
		t.Run(name, func(t *testing.T) {
			_, stderr, code := run(t, strings.Replace(sanBoxOverflowSrc, "OFFSET", "24", 1), uncertified)
			if code != e2eharness.ExitSanitizer || !strings.Contains(stderr, report) {
				t.Errorf("past the box: exit=%d, want %d with the report\nstderr: %s", code, e2eharness.ExitSanitizer, stderr)
			}
			_, stderr, code = run(t, strings.Replace(sanBoxOverflowSrc, "OFFSET", "23", 1), uncertified)
			if code != 16 || strings.Contains(stderr, "fern-sanitizer:") {
				t.Errorf("inside the box: exit=%d, want 16 and no finding\nstderr: %s", code, stderr)
			}
		})
	}
}

func runSanitizeWasm(t *testing.T, src string, env ...string) (string, string, int) {
	t.Helper()
	core := e2eharness.CompileSelfHostSource(t, e2eharness.TargetWasm32Wasi, src, env)
	return runSplit(t, e2eharness.RunWasmCore(t, core))
}

// sanWasmChurnSrc releases 300000 blocks of 16 KiB, 4.9 GB in all. Wasm32 has
// 4 GiB, so the run ends only when the quarantine hands its oldest blocks
// back to the free lists.
const sanWasmChurnSrc = `function hold(n: i32): i32 {
  let b: u8[] = __alloc_u8(n);
  return b.len();
}
function main(): i32 {
  let i: i32 = 0;
  while (i < 300000) {
    hold(16384);
    i = i + 1;
  }
  return 42;
}`

// The wasm runtime reports a use of a freed block and a second raw release as
// the native one does, and its quarantine recycles past its budget.
func TestWasmSanitizeReportsFreedBlocks(t *testing.T) {
	_, stderr, code := runSanitizeWasm(t, sanStaleTouchSrc, "FERN_SANITIZE=1")
	if code != e2eharness.ExitSanitizer || !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)") {
		t.Errorf("stale touch: exit=%d, want %d with the report\nstderr: %s", code, e2eharness.ExitSanitizer, stderr)
	}
	_, stderr, code = runSanitizeWasm(t, sanCleanSrc, "FERN_SANITIZE=1")
	if code != 0 || strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("clean run: exit=%d, want 0 and no finding\nstderr: %s", code, stderr)
	}
	_, stderr, code = runSanitizeWasm(t, sanWasmChurnSrc, "FERN_SANITIZE=1")
	if code != 42 || strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("churn: exit=%d, want 42 and no finding\nstderr: %s", code, stderr)
	}
	const rawTwiceSrc = `function main(): i32 {
  unsafe {
      let a: usize = __alloc(64);
      __free(a, 64);
      __free(a, 64);
      return 0;
  }
}`
	for _, env := range [][]string{{"FERN_SANITIZE=1"}, nil} {
		_, stderr, code = runSanitizeWasm(t, rawTwiceSrc, env...)
		if code == 0 || !strings.Contains(stderr, "fern: release of memory that is already free") {
			t.Errorf("%v: raw double free: exit=%d, want the report\nstderr: %s", env, code, stderr)
		}
	}
}
