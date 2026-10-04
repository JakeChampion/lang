package e2e

import (
	"bytes"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// runBounded starts cmd and waits at most timeout for it. A child that
// outlives the timeout is killed and reported as timedOut with no error;
// otherwise the Wait error comes back as it would from cmd.Run. The child
// runs in its own process group and the timeout kills the group, so it
// reaches what the child forked (a handler program's serve workers) as
// well: they hold its output pipes, and Wait returns only once every
// holder is gone.
func runBounded(cmd *exec.Cmd, timeout time.Duration) (timedOut bool, err error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return false, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return false, err
	case <-time.After(timeout):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return true, nil
	}
}

// TestRunBoundedClassifiesTimeout pins the three outcomes runBounded
// separates: a child that exits cleanly, one that exits non-zero, and
// one that outlives the timeout — the last must come back as timedOut
// with no error, since that is what keeps a slow machine out of a
// coverage-gap count.
func TestRunBoundedClassifiesTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX sleep/true/false")
	}
	if timedOut, err := runBounded(exec.Command("true"), 60*time.Second); timedOut || err != nil {
		t.Errorf("clean exit: timedOut=%v err=%v, want false and nil", timedOut, err)
	}
	if timedOut, err := runBounded(exec.Command("false"), 60*time.Second); timedOut || err == nil {
		t.Errorf("non-zero exit: timedOut=%v err=%v, want false and an error", timedOut, err)
	}
	if timedOut, err := runBounded(exec.Command("sleep", "30"), 100*time.Millisecond); !timedOut || err != nil {
		t.Errorf("outlived the wall: timedOut=%v err=%v, want true and nil", timedOut, err)
	}
	// A grandchild holding the child's stdout pipe keeps Wait from returning
	// until the whole group is killed (#10806). The outer bound turns a
	// regression into a red test rather than a hang.
	held := exec.Command("sh", "-c", "sleep 30 & sleep 30")
	held.Stdout = &bytes.Buffer{}
	result := make(chan bool, 1)
	go func() {
		timedOut, _ := runBounded(held, 100*time.Millisecond)
		result <- timedOut
	}()
	select {
	case timedOut := <-result:
		if !timedOut {
			t.Errorf("forked holder of stdout: timedOut=false, want true")
		}
	case <-time.After(10 * time.Second):
		t.Errorf("forked holder of stdout: runBounded did not return 10 s after a 100 ms wall; the group kill left the pipe open")
	}
}

// x86_64RunnerOrEmpty is how an x86-64 binary runs on this host: natively
// on amd64 Linux, under qemu-x86_64 elsewhere, or not at all.
//
// Deliberately not LookupX86_64Tooling, which reports ok=false without an
// x86-64 gcc on PATH. A test using this never links with one — `fern -target
// x86-64-linux -o out` uses the CLI's own in-process linker — so gating on a
// compiler it does not use would skip its legs on a gcc-less amd64 host,
// where they run perfectly well.
func x86_64RunnerOrEmpty() (runner []string, ok bool) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return nil, true
	}
	if p, err := exec.LookPath("qemu-x86_64"); err == nil {
		return []string{p}, true
	}
	return nil, false
}
