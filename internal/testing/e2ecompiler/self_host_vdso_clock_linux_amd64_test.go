//go:build linux && amd64

package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// TestSelfHostVdsoClockNoSyscall traces the clock program natively and wants
// no clock_gettime (228) from it: every read went through the vDSO.
func TestSelfHostVdsoClockNoSyscall(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(vdsoClockSource), 0o644); err != nil {
		t.Fatal(err)
	}
	counts, code := tracedSyscalls(t, cli.x86Binary(t, src))
	if code != 7 {
		t.Fatalf("traced clock program exited %d, want 7", code)
	}
	if n := counts[syscall.SYS_CLOCK_GETTIME]; n != 0 {
		t.Errorf("the clocks made %d clock_gettime syscalls", n)
	}
}

// tracedSyscalls runs bin under ptrace and counts each syscall it enters,
// returning the counts and its exit code.
func tracedSyscalls(t *testing.T, bin string) (map[uint64]int, int) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cmd := exec.Command(bin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Ptrace: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	var ws syscall.WaitStatus
	if _, err := syscall.Wait4(pid, &ws, 0, nil); err != nil {
		t.Fatal(err)
	}
	counts := map[uint64]int{}
	entering := true
	signal := 0
	for {
		if err := syscall.PtraceSyscall(pid, signal); err != nil {
			t.Fatal(err)
		}
		if _, err := syscall.Wait4(pid, &ws, 0, nil); err != nil {
			t.Fatal(err)
		}
		if ws.Exited() {
			return counts, ws.ExitStatus()
		}
		if ws.Signaled() {
			t.Fatalf("traced program died of %v", ws.Signal())
		}
		signal = 0
		if ws.StopSignal() != syscall.SIGTRAP {
			signal = int(ws.StopSignal())
			continue
		}
		if entering {
			var regs syscall.PtraceRegs
			if err := syscall.PtraceGetRegs(pid, &regs); err != nil {
				t.Fatal(err)
			}
			counts[regs.Orig_rax]++
		}
		entering = !entering
	}
}
