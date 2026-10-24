package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestTimeoutForwardsSignals(t *testing.T) {
	sleepBin := referenceBin(t, "sleep")
	for _, c := range []struct {
		name string
		pre  string
		args []string
		sigs []syscall.Signal
	}{
		{"INT is passed on and kills timeout too", "", []string{"-v", "10", sleepBin, "30"}, []syscall.Signal{syscall.SIGINT}},
		{"TERM in the foreground", "", []string{"--foreground", "-v", "10", sleepBin, "30"}, []syscall.Signal{syscall.SIGTERM}},
		{"QUIT", `ulimit -S -c "$(ulimit -H -c)";`, []string{"-v", "10", sleepBin, "30"}, []syscall.Signal{syscall.SIGQUIT}},
		{"a command killed from elsewhere", "", []string{"-v", "10", "sh", "-c", "kill -USR1 $$"}, nil},
		{"USR2, on term-sig.h's list", "", []string{"-v", "10", sleepBin, "30"}, []syscall.Signal{syscall.SIGUSR2}},
		{"a command that exits on HUP", "", []string{"-v", "10", "sh", "-c", "trap 'exit 7' HUP; sleep 30 & wait"}, []syscall.Signal{syscall.SIGHUP}},
		{"a command that ignores INT finishes", "", []string{"-v", "10", "sh", "-c", "trap '' INT; sleep 1"}, []syscall.Signal{syscall.SIGINT}},
		{"a second INT in group mode", "", []string{"-v", "10", "sh", "-c", "trap '' INT; sleep 1"}, []syscall.Signal{syscall.SIGINT, syscall.SIGINT}},
		{"a second INT in the foreground", "", []string{"--foreground", "-v", "10", "sh", "-c", "trap '' INT; sleep 1"}, []syscall.Signal{syscall.SIGINT, syscall.SIGINT}},
		{"KILL after a forwarded TERM", "", []string{"-v", "-k", "0.5", "10", "sh", "-c", "trap '' TERM; sleep 5"}, []syscall.Signal{syscall.SIGTERM}},
		{"KILL after a forwarded TERM in the foreground", "", []string{"--foreground", "-v", "-k", "0.5", "10", "sh", "-c", "trap '' TERM; sleep 5"}, []syscall.Signal{syscall.SIGTERM}},
		{"ALRM is the deadline", "", []string{"-v", "10", sleepBin, "30"}, []syscall.Signal{syscall.SIGALRM}},
		{"ALRM with preserve-status", "", []string{"-v", "--preserve-status", "10", sleepBin, "30"}, []syscall.Signal{syscall.SIGALRM}},
		{"a forwarded signal, then the deadline", "", []string{"-v", "1", "sh", "-c", "trap '' INT; sleep 3"}, []syscall.Signal{syscall.SIGINT}},
		{"a zero duration still forwards", "", []string{"-v", "0", sleepBin, "30"}, []syscall.Signal{syscall.SIGTERM}},
		{"the -s signal", "", []string{"-v", "-s", "WINCH", "10", "sh", "-c", "trap 'exit 3' WINCH; sleep 30 & wait"}, []syscall.Signal{syscall.SIGWINCH}},
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
		inner := append([]string{cmd.Path}, cmd.Args[1:]...)
		cmd = exec.Command("bash", append([]string{"-c", pre + ` exec -a "$0" "$@"`, cmd.Args[0]}, inner...)...)
	}
	cmd.Env = baseEnv()
	cmd.Dir = t.TempDir()
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

func endOf(st *os.ProcessState) string {
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return "killed by " + ws.Signal().String()
	}
	return "exit " + st.String()
}
