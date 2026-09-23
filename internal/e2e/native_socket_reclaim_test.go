package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestNativeSocketSetupReclaimsDescriptors(t *testing.T) {
	bin := buildFernCLI(t)
	for _, tc := range []struct{ target, backend string }{
		{"x86-64-linux", ""}, {"x86-64-linux", "ssa"}, {"arm64-linux", ""}, {"arm64-linux", "ssa"},
	} {
		t.Run(tc.target+"/"+tc.backend, func(t *testing.T) {
			var run func(string) *exec.Cmd
			if tc.target == "arm64-linux" {
				q := arm64QemuOrEmpty(t)
				run = func(p string) *exec.Cmd { return runArm64Bin(q, p) }
			} else {
				q := x86QemuOrEmpty(t)
				run = func(p string) *exec.Cmd {
					if q != "" {
						return exec.Command(q, p)
					}
					return exec.Command(p)
				}
			}
			checkNativeSocketSetup(t, bin, tc.target, tc.backend, run)
		})
	}
}

func TestArm64DarwinSocketSetupReclaimsDescriptors(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkNativeSocketSetup(t, buildFernCLI(t), "arm64-darwin", "", func(p string) *exec.Cmd { return exec.Command(p) })
}

func checkNativeSocketSetup(t *testing.T, compiler, target, backend string, run func(string) *exec.Cmd) {
	t.Helper()
	connectErrno := e2eharness.NativeSocketConnectError(t)
	for _, operation := range []string{"bind", "connect", "socket"} {
		for _, zero := range []bool{false, true} {
			name := operation
			if zero {
				name += "-zero"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				source, binary := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main")
				if err := os.WriteFile(source, []byte(e2eharness.NativeSocketFailureProbe(operation, target == "arm64-darwin", zero, connectErrno)), 0o644); err != nil {
					t.Fatal(err)
				}
				args := []string{"-target", target, "-o", binary, source}
				if backend != "" {
					args = append([]string{"-backend", backend}, args...)
				}
				if out, err := exec.Command(compiler, args...).CombinedOutput(); err != nil {
					t.Fatalf("build: %v\n%s", err, out)
				}
				e2eharness.CheckNativeSocketFailure(t, run(binary))
			})
		}
	}
}
