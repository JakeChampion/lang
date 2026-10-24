package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDDInterrupt(t *testing.T) {
	duration := regexp.MustCompile(`copied, [0-9.e+-]+ s, [0-9.]+ [kMGTPE]?B/s`)
	type ended struct {
		stderr string
		st     *os.ProcessState
	}
	fifoRun := func(t *testing.T, bin string, ignored bool, args ...string) ended {
		t.Helper()
		dir := t.TempDir()
		fifo := filepath.Join(dir, "slow")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		wrote := make(chan struct{})
		release := make(chan struct{})
		go func() {
			f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
			if err != nil {
				close(wrote)
				return
			}
			f.Write([]byte("abc"))
			close(wrote)
			<-release
			f.Close()
		}()
		argv := crossArgv(bin, append([]string{"if=slow", "of=out"}, args...)...)
		if ignored {
			argv = append([]string{"sh", "-c", `trap '' INT; exec "$0" "$@"`}, argv...)
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = dir
		cmd.Env = baseEnv()
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		<-wrote
		time.Sleep(500 * time.Millisecond)
		if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		if ignored {
			time.Sleep(500 * time.Millisecond)
			close(release)
			waitDD(t, cmd, bin, &stderr)
		} else {
			waitDD(t, cmd, bin, &stderr)
			close(release)
		}
		return ended{duration.ReplaceAllString(stderr.String(), "copied, T s, R/s"), cmd.ProcessState}
	}
	writeRun := func(t *testing.T, bin string) ended {
		t.Helper()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		argv := crossArgv(bin, "if=/dev/zero", "bs=1000", "count=200", "status=noxfer")
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = baseEnv()
		cmd.Stdout = w
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		w.Close()
		time.Sleep(500 * time.Millisecond)
		if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		waitDD(t, cmd, bin, &stderr)
		return ended{stderr.String(), cmd.ProcessState}
	}
	killedByINT := func(st *os.ProcessState) bool {
		ws, ok := st.Sys().(syscall.WaitStatus)
		return ok && ws.Signaled() && ws.Signal() == syscall.SIGINT
	}
	for _, c := range []struct {
		name    string
		ignored bool
		args    []string
		reports int
	}{
		{"default status", false, nil, 1},
		{"noxfer", false, []string{"status=noxfer"}, 1},
		{"none", false, []string{"status=none"}, 0},
		{"bs", false, []string{"bs=2", "status=noxfer"}, 1},
		{"truncated record", false, []string{"conv=block", "cbs=1", "status=noxfer"}, 1},
		{"during skip", false, []string{"skip=10", "bs=1", "status=noxfer"}, 1},
		{"ignored", true, []string{"status=noxfer"}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			want := fifoRun(t, referenceBin(t, "dd"), c.ignored, c.args...)
			got := fifoRun(t, fernBin(t, "dd"), c.ignored, c.args...)
			if killedByINT(want.st) == c.ignored {
				t.Fatalf("gnu: %v, which this case does not expect\nstderr: %q", want.st, want.stderr)
			}
			if killedByINT(got.st) != killedByINT(want.st) || got.st.ExitCode() != want.st.ExitCode() {
				t.Errorf("fern: %v, gnu: %v\nstderr: %q", got.st, want.st, got.stderr)
			}
			if got.stderr != want.stderr {
				t.Errorf("stderr differs with the durations masked\n gnu: %q\nfern: %q", want.stderr, got.stderr)
			}
			if n := strings.Count(got.stderr, "records in\n"); n != c.reports {
				t.Errorf("%d reports, want %d: %q", n, c.reports, got.stderr)
			}
		})
	}
	t.Run("blocked write", func(t *testing.T) {
		want := writeRun(t, referenceBin(t, "dd"))
		got := writeRun(t, fernBin(t, "dd"))
		if !killedByINT(want.st) || !killedByINT(got.st) {
			t.Errorf("fern: %v, gnu: %v; want both killed by SIGINT\nstderr: %q", got.st, want.st, got.stderr)
		}
		if got.stderr != want.stderr {
			t.Errorf("stderr differs\n gnu: %q\nfern: %q", want.stderr, got.stderr)
		}
	})
}

func waitDD(t *testing.T, cmd *exec.Cmd, bin string, stderr *strings.Builder) {
	t.Helper()
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
		t.Fatalf("%s did not finish\nstderr: %q", bin, stderr.String())
	}
}
