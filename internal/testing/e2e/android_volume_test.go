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

// A loop thread retains JNI locals until detach. Each call must release its class.
func TestAndroidVolumeStreamReleasesLocals(t *testing.T) {
	const src = `import "std/android";
pub function volume(ctx: usize, stream: i32): void {
  android.volume_stream(ctx, stream);
}
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>

static int live, calls, expected;
static void require(int ok) { if (!ok) { fprintf(stderr, "JNI contract: live=%d calls=%d\n", live, calls); exit(1); } }
static uintptr_t get_class(uintptr_t env, uintptr_t obj) {
  require(obj == 1234 && live == 0);
  live++;
  return 5678;
}
static uintptr_t method(uintptr_t env, uintptr_t cls, const char *name, const char *sig) {
  require(cls == 5678 && live == 1 && !strcmp(name, "setVolumeControlStream") && !strcmp(sig, "(I)V"));
  return 9012;
}
static void call(uintptr_t env, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  require(obj == 1234 && id == 9012 && live == 1 && (int32_t)args[0] == expected);
  calls++;
}
static void release(uintptr_t env, uintptr_t cls) {
  require(cls == 5678 && live == 1);
  live--;
}
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  void (*volume)(uintptr_t, int32_t) = dlsym(lib, "volume");
  require(volume != NULL);
  uintptr_t table[64] = {0}, activity[8] = {0}, ctx[8] = {0};
  table[31] = (uintptr_t)get_class;
  table[33] = (uintptr_t)method;
  table[63] = (uintptr_t)call;
  table[23] = (uintptr_t)release;
  uintptr_t env = (uintptr_t)table;
  activity[3] = 1234;
  ctx[2] = (uintptr_t)activity;
  ctx[4] = (uintptr_t)&env;
  for (int i = 0; i < 12; i++) {
    expected = 3 + i % 3;
    volume((uintptr_t)ctx, expected);
    require(live == 0 && calls == i + 1);
  }
  return 0;
}
`
	androidJNIMockCheck(t, src, "volume", check)
}

func androidJNIMockCheck(t *testing.T, src, exports, check string) {
	t.Helper()
	host := e2eharness.HostSelfHostTarget()
	if runtime.GOOS != "linux" || host == "" {
		t.Skip("the shared library requires a native Linux target")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc is not on PATH")
	}
	dir := t.TempDir()
	fern := filepath.Join(dir, "probe.fern")
	if err := os.WriteFile(fern, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	so := filepath.Join(dir, "probe.so")
	build := exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-shared", "-export", exports, "-o", so, fern, e2eharness.SelfHostStdlibRoot(t))
	build.Env = e2eharness.ChildEnv()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	c := filepath.Join(dir, "check.c")
	if err := os.WriteFile(c, []byte(check), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "check")
	if out, err := exec.Command(cc, "-o", bin, c, "-ldl").CombinedOutput(); err != nil {
		t.Fatalf("C harness: %v\n%s", err, out)
	}
	// The JNI probe must not call an Android C function on this Linux host.
	stub := e2eharness.LibandroidStub(t, "", src)
	stubPath := filepath.Join(dir, "android.c")
	if err := os.WriteFile(stubPath, []byte(stub), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(cc, "-shared", "-fPIC", "-o", filepath.Join(dir, "libandroid.so"), stubPath).CombinedOutput(); err != nil {
		t.Fatalf("Android C stubs: %v\n%s", err, out)
	}
	libc, err := exec.Command(cc, "-print-file-name=libc.so.6").Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(strings.TrimSpace(string(libc)), filepath.Join(dir, "libc.so")); err != nil {
		t.Fatal(err)
	}
	run := exec.Command(bin, so)
	run.Env = append(os.Environ(), "LD_LIBRARY_PATH="+dir)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("JNI contract: %v\n%s", err, out)
	}
}
