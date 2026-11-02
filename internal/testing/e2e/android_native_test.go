package e2e

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const nativeApp = "examples/android/native"

func TestAndroidNativeFace(t *testing.T) {
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skipf("no self-host target runs natively on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	bin := filepath.Join(t.TempDir(), "face_test")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-o", bin,
		e2eharness.RepoPath(nativeApp, "face_test.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build face_test.fern: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "# fail 0") {
		t.Fatalf("face_test.fern: %v\n%s", err, out)
	}
}

// The NativeActivity library exports ANativeActivity_onCreate alone, and
// needs libandroid.so and libc.so, on both Android ABIs.
func TestAndroidNativeLibrary(t *testing.T) {
	for _, tc := range []struct {
		target  string
		machine elf.Machine
	}{{"arm64-android", elf.EM_AARCH64}, {"x86-64-android", elf.EM_X86_64}} {
		t.Run(tc.target, func(t *testing.T) {
			so := filepath.Join(t.TempDir(), "libnative.so")
			cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", tc.target, "-shared", "-export", "ANativeActivity_onCreate",
				"-o", so, e2eharness.RepoPath(nativeApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t))
			cmd.Env = e2eharness.ChildEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build app.fern: %v\n%s", err, out)
			}
			raw, err := os.ReadFile(so)
			if err != nil {
				t.Fatal(err)
			}
			sharedImageCheck(t, raw, tc.machine)
			f, err := elf.Open(so)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			libs, err := f.ImportedLibraries()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(libs, " ") != "libandroid.so libc.so" {
				t.Errorf("NEEDED = %v, want libandroid.so and libc.so", libs)
			}
			syms, err := f.DynamicSymbols()
			if err != nil {
				t.Fatal(err)
			}
			var exports []string
			for _, s := range syms {
				if s.Section != elf.SHN_UNDEF && elf.ST_TYPE(s.Info) == elf.STT_FUNC {
					exports = append(exports, s.Name)
				}
			}
			if strings.Join(exports, " ") != "ANativeActivity_onCreate" {
				t.Errorf("exports = %v, want ANativeActivity_onCreate alone", exports)
			}
		})
	}
}
