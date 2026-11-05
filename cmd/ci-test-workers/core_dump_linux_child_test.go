//go:build linux

package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestWorkerDescendantsDoNotDumpCore(t *testing.T) {
	parent, err := os.ReadFile("/proc/self/coredump_filter")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", `/bin/sh -c 'read -r f < /proc/self/coredump_filter; [ "$f" = 00000000 ] || exit 52; [ "$(ulimit -c)" = 0 ] || exit 53; printf "%s" "$1"' sh "$1" || exit $?; kill -SEGV $$`, "sh", "arg one")
	isolate(cmd)
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState == nil {
		t.Fatalf("child did not start: %s", out)
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGSEGV || string(out) != "arg one" {
		t.Fatalf("want SIGSEGV with protected descendants and unchanged arguments, got %v, %q", cmd.ProcessState, out)
	}
	after, err := os.ReadFile("/proc/self/coredump_filter")
	if err != nil || string(after) != string(parent) {
		t.Fatalf("parent filter changed: before %q, after %q, error %v", parent, after, err)
	}
}
