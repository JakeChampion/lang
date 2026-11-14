package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// xargsSignalRun is what a run of xargs gives when a signal comes while it
// waits for its commands: the commands that started before they were let
// go, the output as sorted lines, the errors and the exit status.
type xargsSignalRun struct {
	started string
	stdout  string
	stderr  string
	code    int
}

// TestXargsSignalDuringWait sends SIGUSR1 or SIGUSR2 to xargs while it waits
// for its commands. Each command marks its start and then waits for a file
// that the test makes after the signal. GNU xargs adds a command to -P on
// SIGUSR1 and takes one away on SIGUSR2. A wait that the signal interrupts
// goes on, and no command is lost.
func TestXargsSignalDuringWait(t *testing.T) {
	for _, c := range []struct {
		name  string
		sig   syscall.Signal
		procs string
		first []string
	}{
		{"SIGUSR1 adds a command to -P 2", syscall.SIGUSR1, "2", []string{"a", "b"}},
		{"SIGUSR1 adds a command to -P 1", syscall.SIGUSR1, "1", []string{"a"}},
		{"SIGUSR2 does not stop the wait", syscall.SIGUSR2, "2", []string{"a", "b"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			want := runXargsSignal(t, referenceBin(t, "xargs"), c.sig, c.procs, c.first)
			got := runXargsSignal(t, fernBin(t, "xargs"), c.sig, c.procs, c.first)
			if got != want {
				t.Errorf("runs differ\n gnu: %+v\nfern: %+v", want, got)
			}
		})
	}
}

func runXargsSignal(t *testing.T, bin string, sig syscall.Signal, procs string, first []string) xargsSignalRun {
	t.Helper()
	dir := t.TempDir()
	script := `touch started.$0; while [ ! -e go ]; do sleep 0.02; done; echo $0`
	argv := crossArgv(bin, "-n", "1", "-P", procs, "sh", "-c", script)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = baseEnv()
	cmd.Stdin = strings.NewReader("a b c d\n")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	stop := func(msg string) {
		cmd.Process.Kill()
		<-done
		t.Fatalf("%s: %s\nstderr: %q", bin, msg, stderr.String())
	}
	deadline := time.Now().Add(20 * time.Second)
	for _, name := range first {
		for {
			if _, err := os.Stat(filepath.Join(dir, "started."+name)); err == nil {
				break
			}
			if time.Now().After(deadline) {
				stop("command " + name + " did not start")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	time.Sleep(300 * time.Millisecond)
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	marks, _ := filepath.Glob(filepath.Join(dir, "started.*"))
	var started []string
	for _, m := range marks {
		started = append(started, strings.TrimPrefix(filepath.Base(m), "started."))
	}
	sort.Strings(started)
	if err := os.WriteFile(filepath.Join(dir, "go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		stop("did not finish after the commands were let go")
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	sort.Strings(lines)
	return xargsSignalRun{
		started: strings.Join(started, " "),
		stdout:  strings.Join(lines, " "),
		stderr:  stderr.String(),
		code:    cmd.ProcessState.ExitCode(),
	}
}
