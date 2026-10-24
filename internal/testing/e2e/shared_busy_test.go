package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
    return __c_call1(cb, x) as i32;
}

function main(): i32 { return 0; }
`

const sharedBusyLoader = `#include <dlfcn.h>
#include <pthread.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
static int (*spin)(int);
static int (*call_back)(long, long);
static volatile int started;
static int got[2];
static void *run_long(void *a) { started = 1; got[0] = spin(1500000000); return 0; }
static void *run_short(void *a) { got[(long)a] = spin(1000); return 0; }
static long reenter(long x) { return spin((int)x); }
int main(int c, char **v) {
  void *h = dlopen(v[1], RTLD_NOW);
  if (!h) { fprintf(stderr, "%s\n", dlerror()); return 100; }
  spin = (int (*)(int))dlsym(h, "spin");
  call_back = (int (*)(long, long))dlsym(h, "call_back");
  if (!spin || !call_back) return 101;
  pthread_t a, b;
  if (strcmp(v[2], "overlap") == 0) {
    pthread_create(&a, 0, run_long, 0);
    while (!started) {}
    usleep(20000);
    got[1] = spin(1000);
    pthread_join(a, 0);
    return 0;
  }
  if (strcmp(v[2], "serial") == 0) {
    pthread_create(&a, 0, run_short, (void *)0);
    pthread_join(a, 0);
    pthread_create(&b, 0, run_short, (void *)1);
    pthread_join(b, 0);
    return got[0] == got[1] && got[0] == spin(1000) ? 42 : 1;
  }
  if (strcmp(v[2], "reenter") == 0) {
    return call_back((long)&reenter, 1000) == spin(1000) ? 42 : 2;
  }
  return 102;
}
`

func TestSharedLibX86BusyFlag(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	so := selfHostSharedX86(t, sharedBusySrc, "spin", "call_back")
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
	if out, err := exec.Command(gcc, cPath, "-pthread", "-ldl", "-o", ld).CombinedOutput(); err != nil {
		t.Fatalf("gcc loader: %v\n%s", err, out)
	}
	run := func(mode string) (int, string) {
		cmd := exec.Command(ld, soPath, mode)
		out, _ := cmd.CombinedOutput()
		return cmd.ProcessState.ExitCode(), string(out)
	}
	t.Run("overlap_aborts", func(t *testing.T) {
		code, out := run("overlap")
		if code != 134 || !strings.Contains(out, "fern: two threads entered this shared object at once") {
			t.Fatalf("two threads inside at once: exit %d, output %q; want 134 and the busy report", code, out)
		}
	})
	t.Run("serial_runs", func(t *testing.T) {
		if code, out := run("serial"); code != 42 {
			t.Fatalf("two threads one after the other: exit %d, output %q; want 42", code, out)
		}
	})
	t.Run("reentry_runs", func(t *testing.T) {
		if code, out := run("reenter"); code != 42 {
			t.Fatalf("a C callback entering an export on the same thread: exit %d, output %q; want 42", code, out)
		}
	})
}
