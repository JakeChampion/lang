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
	const check = androidBindingsMock + `
static int calls, expected;
static void set_stream(uintptr_t obj, const char *name, const uintptr_t *args) {
  assert(obj == ACTIVITY && !strcmp(name, "setVolumeControlStream") && (int)args[0] == expected);
  calls++;
}
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  assert(lib);
  void (*volume)(uintptr_t, int32_t) = dlsym(lib, "volume");
  assert(volume);
  uintptr_t ctx = mock_ctx();
  on_void = set_stream;
  for (int i = 0; i < 12; i++) {
    expected = 3 + i % 3;
    volume(ctx, expected);
    clean();
    assert(calls == i + 1);
  }
  return 0;
}
`
	androidJNIMockCheck(t, src, "volume", check)
}

func androidJNIMockCheck(t *testing.T, src, exports, check string) {
	t.Helper()
	androidMockCheck(t, src, exports, check, "")
}

// androidMockCheck is androidJNIMockCheck with the C source `defined` for
// the libandroid.so functions that the probe calls on this Linux host.
func androidMockCheck(t *testing.T, src, exports, check, defined string) {
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
	stub := e2eharness.LibandroidStub(t, defined, src)
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
