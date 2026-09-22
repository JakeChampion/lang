package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostNativeSocketSetupReclaimsDescriptors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux native targets")
	}
	checkSelfHostSocketSetup(t, []string{"x86-64-linux", "arm64-linux"})
}

func TestSelfHostArm64DarwinSocketSetupReclaimsDescriptors(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkSelfHostSocketSetup(t, []string{"arm64-darwin"})
}

func checkSelfHostSocketSetup(t *testing.T, targets []string) {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	host := "x86-64-linux"
	if runtime.GOARCH == "arm64" {
		host = "arm64-" + runtime.GOOS
	}
	driver := filepath.Join(dir, "fern")
	if out, err := exec.Command(buildLangBinForInterp(t), "-target", host, "-o", driver, filepath.Join(dir, "fern.fern")).CombinedOutput(); err != nil {
		t.Fatalf("build host compiler: %v\n%s", err, out)
	}
	connectErrno := e2eharness.NativeSocketConnectError(t)
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			var run func(string) *exec.Cmd
			switch target {
			case "arm64-darwin":
				run = func(p string) *exec.Cmd { return exec.Command(p) }
			case "arm64-linux":
				_, q := arm64Tooling(t)
				run = func(p string) *exec.Cmd { return runArm64Bin(q, p) }
			default:
				_, runner := x86_64Tooling(t)
				run = func(p string) *exec.Cmd { return runX86_64Bin(runner, p) }
			}
			for _, operation := range []string{"bind", "connect", "socket"} {
				for _, zero := range []bool{false, true} {
					name := operation
					if zero {
						name += "-zero"
					}
					t.Run(name, func(t *testing.T) {
						d := t.TempDir()
						src, bin := filepath.Join(d, "main.fern"), filepath.Join(d, "main")
						if err := os.WriteFile(src, []byte(e2eharness.NativeSocketFailureProbe(operation, target == "arm64-darwin", zero, connectErrno)), 0o644); err != nil {
							t.Fatal(err)
						}
						cmd := exec.Command(driver, "-target", target, "-o", bin, src)
						cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("self-host build: %v\n%s", err, out)
						}
						e2eharness.CheckNativeSocketFailure(t, run(bin))
					})
				}
			}
		})
	}
}
