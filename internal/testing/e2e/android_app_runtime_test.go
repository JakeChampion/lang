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

func TestAndroidRefusesIOUring(t *testing.T) {
	src := filepath.Join(t.TempDir(), "ring.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return ring_new(1) as i32; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"arm64-android", "x86-64-android", "arm64-linux", "x86-64-linux"} {
		t.Run(target, func(t *testing.T) {
			cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", target, "-emit", "asm", "-o", filepath.Join(t.TempDir(), "ring.s"), src, e2eharness.SelfHostStdlibRoot(t))
			cmd.Env = e2eharness.ChildEnv()
			out, err := cmd.CombinedOutput()
			if strings.HasSuffix(target, "-android") {
				if err == nil || !strings.Contains(string(out), "E066") || !strings.Contains(string(out), "uring") {
					t.Fatalf("want the io_uring capability refusal, got %v\n%s", err, out)
				}
			} else if err != nil {
				t.Fatalf("Linux ring build: %v\n%s", err, out)
			}
		})
	}
}

func TestAndroidAppRuntimeFallback(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Android binaries require a Linux host")
	}
	src := filepath.Join(t.TempDir(), "fallback.fern")
	body := `import "std/async";
function main(): i32 {
  let d: async.RealDriver = async.real_driver_io_uring();
  if (d.io_uring()) { return 1; }
  if (poll([-1], 0) != -1) { return 2; }
  if (tcp_accept(-1) != -9) { return 3; }
  let handle: i32 = d.handle();
  if (handle < 0) { return 4; }
  if (tcp_close(handle) != 0) { return 5; }
  return 0;
}
`
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"arm64-android", "x86-64-android"} {
		t.Run(target, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "fallback")
			build := exec.Command(e2eharness.SelfHostCLI(t), "-target", target, "-o", bin, src, e2eharness.SelfHostStdlibRoot(t))
			build.Env = e2eharness.ChildEnv()
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, out)
			}
			cmd := exec.Command(bin)
			if target == "x86-64-android" {
				cmd = e2eharness.RunX86_64Bin(e2eharness.X86_64Runner(t), bin)
			} else if runner := e2eharness.Arm64Runner(t); runner != "" {
				cmd = exec.Command(runner, bin)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
		})
	}
}
