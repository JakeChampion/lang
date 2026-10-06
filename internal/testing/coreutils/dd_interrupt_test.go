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

// SIGINT mid-copy (#11698): GNU dd writes its statistics report and then dies
// of the signal. Its handler interrupts a blocked read or write, so the report
// comes at once even when nothing more will arrive.
//
// Each case runs GNU and Fern the same way and compares the two: stderr with
// the durations masked, and how the process ended — killed by SIGINT, or, for
// a SIGINT ignored at the start, an ordinary exit once the input closes.
func TestDDInterrupt(t *testing.T) {
	duration := regexp.MustCompile(`copied, [0-9.e+-]+ s, [0-9.]+ [kMGTPE]?B/s`)
	type ended struct {
		stderr string
		st     *os.ProcessState
	}
	// fifoRun has dd read `abc` from a FIFO and block on the next read, where
	// SIGINT arrives. The writer closes the FIFO only once dd has ended: a
	// pipe read that wakes to find the writer gone returns EOF even with the
	// signal pending, which would race the report against `cannot skip` and
	// the end of input. `ignored` starts dd with SIGINT ignored, as a shell
	// without job control starts a background command; then the FIFO closes
	// after a pause, and dd finishes the copy.
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
		// Long enough for dd to have read the three bytes and to be
		// waiting on the next read, under qemu too.
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
	// writeRun blocks dd on a write instead: its output is a pipe the test
	// holds open and never reads, so the copy stops when the pipe is full.
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
		// reports is how many record-count reports stderr carries.
		reports int
	}{
		{"default status", false, nil, 1},
		{"noxfer", false, []string{"status=noxfer"}, 1},
		{"none", false, []string{"status=none"}, 0},
		// bs= writes each record as it is read, so one is out as well.
		{"bs", false, []string{"bs=2", "status=noxfer"}, 1},
		// The truncation of the record already read is in the report.
		{"truncated record", false, []string{"conv=block", "cbs=1", "status=noxfer"}, 1},
		// The skip reads the pipe too, and nothing has been copied yet.
		{"during skip", false, []string{"skip=10", "bs=1", "status=noxfer"}, 1},
		// Ignored at the start, SIGINT stays ignored and the copy finishes.
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

// waitDD waits for a dd the test has signalled, and fails rather than hang
// when it does not end.
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
