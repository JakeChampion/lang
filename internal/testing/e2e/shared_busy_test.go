package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

function main(): i32 { return 0; }
`

const sharedBusyLoader = `#include <dlfcn.h>
#include <pthread.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
static int (*spin)(int);
static int (*build)(int);
static int (*call_back)(long, long);
static volatile int started;
static volatile int bad;
static int got[2];
static int expect(int n) { int acc = 0; for (int i = 0; i < n; i++) acc = (acc + i % 7) % 1000; return acc; }
static void *run_long(void *a) { started = 1; got[0] = spin(30000000); return 0; }
static void *run_short(void *a) { got[(long)a] = spin(1000); return 0; }
static long reenter(long x) { return spin((int)x); }
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
  if (!spin || !build || !call_back) return 101;
  pthread_t a, b, many[6];
  if (strcmp(v[2], "overlap") == 0) {
    pthread_create(&a, 0, run_long, 0);
    while (!started) {}
    usleep(20000);
    got[1] = spin(1000);
    pthread_join(a, 0);
    return got[0] == expect(30000000) && got[1] == expect(1000) ? 42 : 1;
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
  if (strcmp(v[2], "reenter") == 0) {
    return call_back((long)&reenter, 1000) == expect(1000) ? 42 : 2;
  }
  return 102;
}
`

// Threads that enter the shared object at once take turns on __fern_busy:
// a thread waits while another one runs Fern code. A __c_call gives the turn
// back for the call, so a C callback on the same thread still runs.
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
			so := selfHostShared(t, l.target, sharedBusySrc, "spin", "build", "call_back")
			dir := t.TempDir()
			soPath := filepath.Join(dir, "libfern.so")
			if err := os.WriteFile(soPath, so, 0o755); err != nil {
				t.Fatal(err)
			}
			cPath := filepath.Join(dir, "loader.c")
			if err := os.WriteFile(cPath, []byte(sharedBusyLoader), 0o644); err != nil {
				t.Fatal(err)
			}
			ld := filepath.Join(dir, "loader")
			if out, err := exec.Command(l.cc, cPath, "-pthread", "-ldl", "-o", ld).CombinedOutput(); err != nil {
				t.Fatalf("%s loader: %v\n%s", l.cc, err, out)
			}
			for _, mode := range []string{"overlap", "contend", "serial", "reenter"} {
				t.Run(mode, func(t *testing.T) {
					cmd := exec.Command(ld, soPath, mode)
					if l.runner != "" {
						cmd = exec.Command(l.runner, ld, soPath, mode)
					}
					cmd.Env = append(os.Environ(), l.env...)
					out, _ := cmd.CombinedOutput()
					if code := cmd.ProcessState.ExitCode(); code != 42 {
						t.Fatalf("exit %d, output %q; want 42", code, out)
					}
				})
			}
		})
	}
}
