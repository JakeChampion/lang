package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestWithoutCoreDump(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux per-process core filter")
	}
	parent, err := os.ReadFile("/proc/self/coredump_filter")
	if err != nil {
		t.Fatal(err)
	}
	var parentLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CORE, &parentLimit); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(t.TempDir(), "probe.sh")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\n"+`read -r f < /proc/self/coredump_filter; [ "$f" = 00000000 ] || exit 52; [ "$(ulimit -c)" = 0 ] || exit 53; printf '%s' "$1"; kill -SEGV $$`), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		cmd  *exec.Cmd
	}{
		{"direct", WithoutCoreDump(exec.Command(probe, "arg one"))},
		{"x86-native", RunX86_64Bin(nil, probe, "arg one")},
		{"x86-runner", RunX86_64Bin([]string{"/bin/sh"}, probe, "arg one")},
		{"arm64-native", RunArm64Bin("", probe, "arg one")},
		{"arm64-runner", RunArm64Bin("/bin/sh", probe, "arg one")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.cmd
			if parentLimit.Max > 0 {
				args := append([]string{"-c", `ulimit -c 1 && exec "$@"`, "sh", cmd.Path}, cmd.Args[1:]...)
				cmd = exec.Command("/bin/sh", args...)
			}
			out, _ := cmd.CombinedOutput()
			if cmd.ProcessState == nil {
				t.Fatalf("child did not start: %s", out)
			}
			status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGSEGV {
				t.Fatalf("want SIGSEGV with a cleared filter, got %v\n%s", cmd.ProcessState, out)
			}
			if string(out) != "arg one" {
				t.Errorf("the child got %q, want its argument unchanged", out)
			}
		})
	}
	after, err := os.ReadFile("/proc/self/coredump_filter")
	if err != nil || string(after) != string(parent) {
		t.Fatalf("parent filter changed: before %q, after %q, error %v", parent, after, err)
	}
	var afterLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CORE, &afterLimit); err != nil || afterLimit != parentLimit {
		t.Fatalf("parent core limit changed: before %v, after %v, error %v", parentLimit, afterLimit, err)
	}
}
