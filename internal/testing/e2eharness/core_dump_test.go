package e2eharness

import (
	"os"
	"os/exec"
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
	cmd := WithoutCoreDump(exec.Command("/bin/sh", "-c", `read -r f < /proc/self/coredump_filter; [ "$f" = 00000000 ] || exit 52; printf '%s' "$1"; kill -SEGV $$`, "sh", "arg one"))
	out, _ := cmd.CombinedOutput()
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGSEGV {
		t.Fatalf("want SIGSEGV with a cleared filter, got %v\n%s", cmd.ProcessState, out)
	}
	if string(out) != "arg one" {
		t.Errorf("the child got %q, want its argument unchanged", out)
	}
	after, err := os.ReadFile("/proc/self/coredump_filter")
	if err != nil || string(after) != string(parent) {
		t.Fatalf("parent filter changed: before %q, after %q, error %v", parent, after, err)
	}
}
