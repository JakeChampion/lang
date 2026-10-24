package e2ecompiler

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

func TestSelfHostCPUProfileMaskHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "fern-profile-mask-exec" {
		return
	}
	bin := os.Args[len(os.Args)-1]
	if runtime.GOOS != "linux" {
		t.Fatal("signal-mask helper requires Linux")
	}
	runtime.LockOSThread()
	mask := uint64(1 << (syscall.SIGPROF - 1))
	nr := uintptr(14)
	if runtime.GOARCH == "arm64" {
		nr = 135
	}
	_, _, err := syscall.RawSyscall6(nr, 0, uintptr(unsafe.Pointer(&mask)), 0, 8, 0, 0)
	if err != 0 {
		os.Exit(120)
	}
	if err := syscall.Exec(bin, []string{bin}, os.Environ()); err != nil {
		os.Exit(121)
	}
}

func TestSelfHostCPUProfileLinuxShutdown(t *testing.T) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("native Linux profiler target")
	}
	cli := buildRuntimeCLI(t)
	target := "x86-64-linux"
	if runtime.GOARCH == "arm64" {
		target = "arm64-linux"
	}
	bin := compileCPUProfile(t, cli, cpuProfileHot, target, "cpu")
	t.Run("inherited_blocked_sigprof", func(t *testing.T) {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(self, "-test.run=^TestSelfHostCPUProfileMaskHelper$", "--", "fern-profile-mask-exec", bin)
		stdout, stderr := runCPUProfile(t, cmd, 7)
		if stdout != "-1611139248354046463\n" {
			t.Fatalf("stdout %q", stdout)
		}
		checkCPUProfileHot(t, stderr)
	})
	short := compileCPUProfile(t, cli, `function main(): i32 { return 7; }`, target, "cpu")
	for _, device := range []string{"/dev/full", "closed_pipe"} {
		t.Run(device, func(t *testing.T) {
			var sink *os.File
			if device == "closed_pipe" {
				r, w, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				_ = r.Close()
				sink = w
			} else {
				var err error
				sink, err = os.OpenFile(device, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
			}
			defer sink.Close()
			cmd := exec.Command(short)
			cmd.Stderr = sink
			_ = cmd.Run()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 7 {
				t.Fatalf("failed profile output changes exit: %v", cmd.ProcessState)
			}
		})
	}
	t.Run("fork_child", func(t *testing.T) {
		fork := "__syscall3(57, 0, 0, 0)"
		if runtime.GOARCH == "arm64" {
			fork = "__syscall6(220, 17, 0, 0, 0, 0, 0)"
		}
		src := `function main(): i32 { let child: i64 = ` + fork + `;
  if (child < (0 as i64)) { return 12; }
  if (child == (0 as i64)) { exit(0); }
  return 7;
}`
		forkBin := compileCPUProfile(t, cli, src, target, "cpu")
		_, stderr := runCPUProfile(t, exec.Command(forkBin), 7)
		if strings.Count(stderr, "# fern CPU profile samples=") != 1 {
			t.Fatalf("child duplicated inherited profile: %s", stderr)
		}
	})
}
