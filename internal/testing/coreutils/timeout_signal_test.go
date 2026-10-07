package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A signal that reaches timeout itself (#11698). GNU passes SIGINT, SIGTERM
// and the rest of its term-sig.h list on to the command, then dies of the
// signal if the command did; SIGALRM from outside is the deadline passing.
// Each case starts GNU and Fern the same way, signals timeout once the
// command is running, and compares how each ended — the exit status, or the
// signal that killed it — and stderr, where -v names every signal sent.
//
// stderr goes to a file rather than a pipe: with --foreground the command's
// own children are not signalled and can outlive timeout, and a pipe they
// hold open would keep the read waiting for them.
func TestTimeoutForwardsSignals(t *testing.T) {
	sleepBin := referenceBin(t, "sleep")
	for _, c := range []struct {
		name string
		// pre runs timeout under `sh -c`, with this prefix to its exec.
		pre  string
		args []string
		sigs []syscall.Signal
	}{
		{"INT is passed on and kills timeout too", "", []string{"-v", "10", sleepBin, "30"}, []syscall.Signal{syscall.SIGINT}},
		{"TERM in the foreground", "", []string{"--foreground", "-v", "10", sleepBin, "30"}, []syscall.Signal{syscall.SIGTERM}},
		{"QUIT", "", []string{"-v", "10", sleepBin, "30"}, []syscall.Signal{syscall.SIGQUIT}},
		{"USR2, on term-sig.h's list", "", []string{"-v", "10", sleepBin, "30"}, []syscall.Signal{syscall.SIGUSR2}},
		{"a command that exits on HUP", "", []string{"-v", "10", "sh", "-c", "trap 'exit 7' HUP; sleep 30 & wait"}, []syscall.Signal{syscall.SIGHUP}},
		{"a command that ignores INT finishes", "", []string{"-v", "10", "sh", "-c", "trap '' INT; sleep 1"}, []syscall.Signal{syscall.SIGINT}},
		// In group mode the first INT sets timeout's own to ignored.
		{"a second INT in group mode", "", []string{"-v", "10", "sh", "-c", "trap '' INT; sleep 1"}, []syscall.Signal{syscall.SIGINT, syscall.SIGINT}},
		{"a second INT in the foreground", "", []string{"--foreground", "-v", "10", "sh", "-c", "trap '' INT; sleep 1"}, []syscall.Signal{syscall.SIGINT, syscall.SIGINT}},
		// -k's grace period starts at the first signal sent, forwarded or not.
		{"KILL after a forwarded TERM", "", []string{"-v", "-k", "0.5", "10", "sh", "-c", "trap '' TERM; sleep 5"}, []syscall.Signal{syscall.SIGTERM}},
		{"KILL after a forwarded TERM in the foreground", "", []string{"--foreground", "-v", "-k", "0.5", "10", "sh", "-c", "trap '' TERM; sleep 5"}, []syscall.Signal{syscall.SIGTERM}},
		{"ALRM is the deadline", "", []string{"-v", "10", sleepBin, "30"}, []syscall.Signal{syscall.SIGALRM}},
		{"ALRM with preserve-status", "", []string{"-v", "--preserve-status", "10", sleepBin, "30"}, []syscall.Signal{syscall.SIGALRM}},
		{"a forwarded signal, then the deadline", "", []string{"-v", "1", "sh", "-c", "trap '' INT; sleep 3"}, []syscall.Signal{syscall.SIGINT}},
		{"a zero duration still forwards", "", []string{"-v", "0", sleepBin, "30"}, []syscall.Signal{syscall.SIGTERM}},
		{"the -s signal", "", []string{"-v", "-s", "WINCH", "10", "sh", "-c", "trap 'exit 3' WINCH; sleep 30 & wait"}, []syscall.Signal{syscall.SIGWINCH}},
		// Ignored when timeout starts, INT is not caught: the signal does
		// nothing and the command runs out its second.
		{"INT ignored at the start", "trap '' INT;", []string{"-v", "10", sleepBin, "1"}, []syscall.Signal{syscall.SIGINT}},
	} {
		t.Run(c.name, func(t *testing.T) {
			wantSt, wantErr := forwardRun(t, referenceBin(t, "timeout"), c.pre, c.args, c.sigs)
			gotSt, gotErr := forwardRun(t, fernBin(t, "timeout"), c.pre, c.args, c.sigs)
			if endOf(gotSt) != endOf(wantSt) {
				t.Errorf("fern %s, gnu %s\nstderr: %q", endOf(gotSt), endOf(wantSt), gotErr)
			}
			if gotErr != wantErr {
				t.Errorf("stderr differs\n gnu: %q\nfern: %q", wantErr, gotErr)
			}
		})
	}
}

// forwardRun starts timeout as `timeout ARGS…`, sends `sigs` 300 ms apart
// once the command has had half a second to start, and waits for it.
func forwardRun(t *testing.T, bin, pre string, args []string, sigs []syscall.Signal) (*os.ProcessState, string) {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Path = bin
	cmd.Args = append([]string{"timeout"}, args...)
	if xp := crossPrefix(); len(xp) > 0 {
		emu, err := exec.LookPath(xp[0])
		if err != nil {
			t.Fatalf("FERN_COREUTILS_QEMU names %s: %v", xp[0], err)
		}
		cmd.Path = emu
		cmd.Args = append(append(append([]string{xp[0]}, xp[1:]...), "-0", "timeout", bin), args...)
	}
	if pre != "" {
		// exec -a keeps the name timeout reports; the shell replaces itself,
		// so the pid signalled below is timeout's.
		inner := append([]string{cmd.Path}, cmd.Args[1:]...)
		cmd = exec.Command("bash", append([]string{"-c", pre + ` exec -a "$0" "$@"`, cmd.Args[0]}, inner...)...)
	}
	cmd.Env = baseEnv()
	errPath := filepath.Join(t.TempDir(), "stderr")
	errFile, err := os.Create(errPath)
	if err != nil {
		t.Fatal(err)
	}
	defer errFile.Close()
	cmd.Stderr = errFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	for _, s := range sigs {
		cmd.Process.Signal(s)
		time.Sleep(300 * time.Millisecond)
	}
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Fatalf("%s did not finish", bin)
	}
	out, err := os.ReadFile(errPath)
	if err != nil {
		t.Fatal(err)
	}
	return cmd.ProcessState, string(out)
}

// endOf names how a process ended: the signal that killed it, or its exit
// status.
func endOf(st *os.ProcessState) string {
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return "killed by " + ws.Signal().String()
	}
	return "exit " + st.String()
}
