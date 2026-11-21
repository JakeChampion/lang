package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const sharedBusySrc = `function spin(n: i32): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < n) {
        acc = (acc + i % 7) % 1000;
        i = i + 1;
    }
    return acc;
}

function call_back(cb: usize, x: usize): i32 {
  unsafe {
      return __c_call1(cb, x) as i32;
  }
}

// Heap traffic: each call allocates, grows and frees a string.
function build(n: i32): i32 {
  let out: string = "";
  let i: i32 = 0;
  while (i < n) {
    out = out + "ab";
    i = i + 1;
  }
  return out.len();
}

// It touches no heap, so its entry takes no turn: another thread can call it
// while a thread runs Fern code.
@norc
function tally(p: usize, n: i64): i64 {
  unsafe {
    return __atomic_fetch_add(p, n);
  }
}

function main(): i32 { return 0; }
`

const sharedBusyLoader = `#include <dlfcn.h>
#include <pthread.h>
#include <stdio.h>
#include <string.h>
#include <time.h>
#include <unistd.h>
static int (*spin)(int);
static int (*build)(int);
static int (*call_back)(long, long);
static long (*tally)(long *, long);
static volatile int started;
static volatile int long_done;
static volatile int bad;
static int got[2];
static int expect(int n) { int acc = 0; for (int i = 0; i < n; i++) acc = (acc + i % 7) % 1000; return acc; }
static void *run_long(void *a) { started = 1; got[0] = spin(30000000); long_done = 1; return 0; }
static void *run_short(void *a) { got[(long)a] = spin(1000); return 0; }
static long reenter(long x) { return spin((int)x); }
static volatile int stop;
static volatile long hogged;
// Enters the shared object again at once after each return, for at most 20
// seconds. Each turn is some milliseconds of Fern code.
static void *run_hog(void *a) {
  struct timespec t0, t;
  clock_gettime(CLOCK_MONOTONIC, &t0);
  started = 1;
  while (!stop) {
    for (int i = 0; i < 10 && !stop; i++) { spin(1000000); hogged++; }
    clock_gettime(CLOCK_MONOTONIC, &t);
    if (t.tv_sec - t0.tv_sec > 20) break;
  }
  return 0;
}
static void *run_many(void *a) {
  for (int i = 0; i < 300; i++) {
    if (spin(100 + i) != expect(100 + i)) bad = 1;
    if (build(10 + i) != 2 * (10 + i)) bad = 2;
    if (call_back((long)&reenter, 50) != expect(50)) bad = 3;
  }
  return 0;
}
int main(int c, char **v) {
  void *h = dlopen(v[1], RTLD_NOW);
  if (!h) { fprintf(stderr, "%s\n", dlerror()); return 100; }
  spin = (int (*)(int))dlsym(h, "spin");
  build = (int (*)(int))dlsym(h, "build");
  call_back = (int (*)(long, long))dlsym(h, "call_back");
  tally = (long (*)(long *, long))dlsym(h, "tally");
  if (!spin || !build || !call_back || !tally) return 101;
  pthread_t a, b, many[6];
  if (strcmp(v[2], "overlap") == 0) {
    pthread_create(&a, 0, run_long, 0);
    while (!started) {}
    usleep(20000);
    got[1] = spin(1000);
    pthread_join(a, 0);
    return got[0] == expect(30000000) && got[1] == expect(1000) ? 42 : 1;
  }
  if (strcmp(v[2], "norc") == 0) {
    static long counter = 5;
    pthread_create(&a, 0, run_long, 0);
    while (!started) {}
    usleep(20000);
    long first = tally(&counter, 2);
    long second = tally(&counter, 3);
    int early = !long_done;
    pthread_join(a, 0);
    fprintf(stderr, "norc: %ld %ld %ld, before the long call ended: %d\n", first, second, counter, early);
    return first == 5 && second == 7 && counter == 10 && early && got[0] == expect(30000000) ? 42 : 6;
  }
  if (strcmp(v[2], "contend") == 0) {
    for (int i = 0; i < 6; i++) pthread_create(&many[i], 0, run_many, 0);
    for (int i = 0; i < 6; i++) pthread_join(many[i], 0);
    return bad == 0 ? 42 : bad;
  }
  if (strcmp(v[2], "serial") == 0) {
    pthread_create(&a, 0, run_short, (void *)0);
    pthread_join(a, 0);
    pthread_create(&b, 0, run_short, (void *)1);
    pthread_join(b, 0);
    return got[0] == got[1] && got[0] == expect(1000) ? 42 : 1;
  }
  if (strcmp(v[2], "fair") == 0) {
    pthread_create(&a, 0, run_hog, 0);
    while (!started) {}
    usleep(20000);
    struct timespec t0, t1;
    clock_gettime(CLOCK_MONOTONIC, &t0);
    for (int i = 0; i < 50; i++) {
      if (spin(10) != expect(10)) bad = 4;
    }
    clock_gettime(CLOCK_MONOTONIC, &t1);
    stop = 1;
    pthread_join(a, 0);
    long ms = (t1.tv_sec - t0.tv_sec) * 1000 + (t1.tv_nsec - t0.tv_nsec) / 1000000;
    fprintf(stderr, "fair: 50 turns in %ld ms beside %ld turns of the other thread\n", ms, hogged);
    return bad == 0 && ms < 3000 ? 42 : 5;
  }
  if (strcmp(v[2], "reenter") == 0) {
    return call_back((long)&reenter, 1000) == expect(1000) ? 42 : 2;
  }
  return 102;
}
`

// Threads that enter the shared object at once take turns on __fern_busy:
// a thread waits while another one runs Fern code. A __c_call gives the turn
// back for the call, so a C callback on the same thread still runs.
// The turn is fair: a thread that waits gets the next turn, also when the
// thread that had it asks for it again at once. A `@norc` export takes no
// turn, so it returns while another thread runs Fern code.
func TestSharedLibBusyLock(t *testing.T) {
	type leg struct {
		target, cc, runner string
		env                []string
	}
	var legs []leg
	if gcc, err := exec.LookPath("gcc"); err == nil && runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		legs = append(legs, leg{"x86-64-linux", gcc, "", nil})
	}
	if runtime.GOOS == "linux" {
		gcc, qemu := e2eharness.Arm64Tooling(t)
		var env []string
		if qemu != "" {
			env = []string{"QEMU_LD_PREFIX=/usr/aarch64-linux-gnu"}
		}
		legs = append(legs, leg{"arm64-linux", gcc, qemu, env})
	}
	if len(legs) == 0 {
		t.Skip("no C toolchain for a Linux shared object on this host")
	}
	for _, l := range legs {
		t.Run(l.target, func(t *testing.T) {
			if strings.HasPrefix(l.target, "arm64") {
				e2eharness.Arm64CLibc(t, l.cc)
			}
			dir := t.TempDir()
			ld := buildSharedLoader(t, l.cc, dir, sharedBusyLoader)
			plain := writeSharedObject(t, dir, "libfern.so", selfHostShared(t, l.target, sharedBusySrc, "spin", "build", "call_back", "tally"))
			sanitized := writeSharedObject(t, dir, "libfern-san.so", selfHostSharedEnv(t, l.target, sharedBusySrc, []string{"FERN_SANITIZE=1"}, "spin", "build", "call_back", "tally"))
			for _, mode := range []string{"overlap", "contend", "serial", "reenter", "fair", "norc"} {
				t.Run(mode, func(t *testing.T) {
					out, code := runSharedLoader(l.runner, l.env, ld, plain, mode)
					if code != 42 {
						t.Fatalf("exit %d, output %q; want 42", code, out)
					}
					t.Logf("%s", out)
				})
			}
			// Under -sanitize each thread that takes the turn owns the heap,
			// so no turn is a finding. The fair mode times its turns, and the
			// sanitizer slows them.
			for _, mode := range []string{"overlap", "contend", "serial", "reenter", "norc"} {
				t.Run("sanitize/"+mode, func(t *testing.T) {
					out, code := runSharedLoader(l.runner, l.env, ld, sanitized, mode)
					if code != 42 || strings.Contains(out, "fern-sanitizer:") {
						t.Fatalf("exit %d, output %q; want 42 and no finding", code, out)
					}
				})
			}
		})
	}
}

func buildSharedLoader(t *testing.T, cc, dir, src string) string {
	t.Helper()
	cPath := filepath.Join(dir, "loader.c")
	if err := os.WriteFile(cPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ld := filepath.Join(dir, "loader")
	if out, err := exec.Command(cc, cPath, "-pthread", "-ldl", "-o", ld).CombinedOutput(); err != nil {
		t.Fatalf("%s loader: %v\n%s", cc, err, out)
	}
	return ld
}

func writeSharedObject(t *testing.T, dir, name string, so []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, so, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func runSharedLoader(runner string, env []string, ld string, args ...string) (string, int) {
	cmd := exec.Command(ld, args...)
	if runner != "" {
		cmd = exec.Command(runner, append([]string{ld}, args...)...)
	}
	cmd.Env = append(os.Environ(), env...)
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

// sharedStraySrc makes a thread with a raw clone inside an export. The
// thread has its own stack and thread pointer and takes no turn on the busy
// lock. Then it allocates. CLONE is the clone call, which names the thread
// pointer in a different argument on each ISA.
func sharedStraySrc(mmap, nanosleep int, clone string) string {
	r := strings.NewReplacer("MMAP", strconv.Itoa(mmap), "NANOSLEEP", strconv.Itoa(nanosleep), "CLONE", clone)
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

function stray(): i32 {
  let ts: usize = __raw_alloc(16);
  __raw_store_ptr(ts, 0, 0 as usize);
  __raw_store_ptr(ts, 1, 10000000 as usize);
  let stack: i64 = __syscall6(MMAP, 0, 65536, 3, 34, 0 - 1, 0) + 65536;
  let tls: i64 = __syscall6(MMAP, 0, 4096, 3, 34, 0 - 1, 0);
  let r: i64 = CLONE;
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

function main(): i32 { return 0; }
`)
}

const sharedStrayLoader = `#include <dlfcn.h>
#include <stdio.h>
int main(int c, char **v) {
  void *h = dlopen(v[1], RTLD_NOW);
  if (!h) { fprintf(stderr, "%s\n", dlerror()); return 100; }
  int (*stray)(void) = (int (*)(void))dlsym(h, "stray");
  if (!stray) return 101;
  return stray() == 6 ? 42 : 1;
}
`

// A thread that runs Fern code in a shared object without the turn on the
// busy lock is a sanitizer finding at its first allocation.
func TestSharedLibSanitizeStrayThread(t *testing.T) {
	type leg struct {
		target, cc, runner string
		env                []string
		src                string
	}
	// CLONE_VM, CLONE_FS, CLONE_FILES, CLONE_SIGHAND, CLONE_THREAD,
	// CLONE_SYSVSEM and CLONE_SETTLS.
	const flags = "855808"
	var legs []leg
	if gcc, err := exec.LookPath("gcc"); err == nil && runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		legs = append(legs, leg{"x86-64-linux", gcc, "", nil, sharedStraySrc(9, 35, "__syscall6(56, "+flags+", stack, 0, 0, tls, 0)")})
	}
	if runtime.GOOS == "linux" {
		gcc, qemu := e2eharness.Arm64Tooling(t)
		var env []string
		if qemu != "" {
			env = []string{"QEMU_LD_PREFIX=/usr/aarch64-linux-gnu"}
		}
		legs = append(legs, leg{"arm64-linux", gcc, qemu, env, sharedStraySrc(222, 101, "__syscall6(220, "+flags+", stack, 0, tls, 0, 0)")})
	}
	if len(legs) == 0 {
		t.Skip("no C toolchain for a Linux shared object on this host")
	}
	for _, l := range legs {
		t.Run(l.target, func(t *testing.T) {
			if strings.HasPrefix(l.target, "arm64") {
				e2eharness.Arm64CLibc(t, l.cc)
			}
			dir := t.TempDir()
			ld := buildSharedLoader(t, l.cc, dir, sharedStrayLoader)
			so := compileRuntimeFloor(t, l.target, l.src, []string{"-shared", "-export", "stray"}, "FERN_SANITIZE=1")
			out, code := runSharedLoader(l.runner, l.env, ld, so)
			checkSecondContextReport(t, out, code)
		})
	}
}
