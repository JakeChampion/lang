package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const glCheckLoader = `#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
int main(int c, char** v) {
  void* h = dlopen(v[1], RTLD_NOW); if (!h) { fprintf(stderr, "%s\n", dlerror()); return 100; }
  int (*check)(void) = (int(*)(void)) dlsym(h, "check");
  if (!check) return 101;
  int got = check();
  printf("%d\n", got);
  return got == atoi(v[2]) ? 42 : 1;
}
`

// runGLCheck builds src as a shared object for the host and calls its
// check() with Mesa's OpenGL ES 2, from the root of the repository. check()
// must return want.
func runGLCheck(t *testing.T, src, want string) {
	t.Helper()
	libs := e2eharness.HostGL(t)
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skip("no self-host target runs natively on this host")
	}
	cc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc is not on PATH")
	}
	dir := t.TempDir()
	so := filepath.Join(dir, "libcheck.so")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-shared", "-export", "check", "-o", so, e2eharness.RepoPath(src), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", src, err, out)
	}
	loader := filepath.Join(dir, "loader.c")
	if err := os.WriteFile(loader, []byte(glCheckLoader), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "loader")
	if out, err := exec.Command(cc, "-o", bin, loader, "-ldl").CombinedOutput(); err != nil {
		t.Fatalf("cc: %v\n%s", err, out)
	}
	run := exec.Command(bin, so, want)
	run.Dir = e2eharness.RepoPath()
	run.Env = e2eharness.HostGLEnv(libs)
	out, err := run.CombinedOutput()
	if code := run.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("check() of %s: exit %d (%v), want %s; each digit is one check:\n%s", src, code, err, want, strings.TrimSpace(string(out)))
	}
}

// std/android/gl clears, draws a premultiplied texture, a stretched part,
// an alpha ramp, a band of rows, and a layer at half alpha, and reads back
// the pixels that OpenGL ES 2 drew.
func TestAndroidGLCanvas(t *testing.T) {
	runGLCheck(t, "tests/android/gl/check.fern", "111111")
}
