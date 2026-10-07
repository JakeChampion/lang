package coreutils

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/tools/tty"
)

// Pseudo-terminals for the corpus, so a case can ask a utility what it does
// when it is talking to a terminal rather than to a pipe.
//
// Three things need one. `ls` lays its output out in columns and asks the
// terminal how wide it is; `nohup` decides which of fds 0, 1 and 2 to
// redirect by asking which are terminals, and its message names the ones it
// redirected; `stty` is a terminal utility and nothing else. Against a pipe
// every one of those questions answers no, so the piped corpus reaches the
// dull half of each.
//
// Linux and Darwin, through internal/tools/tty.OpenPTY: TIOCGPTN and
// /dev/pts/N on the one, grant, unlock and TIOCPTYGNAME on the other.

// ptyRows and ptyCols are the window size every tty case runs at. A fresh
// pty carries 0x0, which is not a size any utility can lay out against — it
// falls back to COLUMNS or to 80 — so the case would be measuring the
// fallback rather than the terminal. 24x80 is the conventional default and
// wide enough for several columns.
const (
	ptyRows = 24
	ptyCols = 80
)

// openPty returns a fresh master/slave pair, sized ptyRows x ptyCols.
//
// The caller closes both. The SLAVE goes to the child and the caller closes
// its own copy once the child has started — while the parent still holds one,
// a read of the master cannot see the end of the child's output.
func openPty(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("a pty case needs Linux or Darwin")
	}
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("open a pseudo-terminal: %v", err)
	}
	if err := tty.SetWindowSize(int(slave.Fd()), ptyRows, ptyCols); err != nil {
		master.Close()
		slave.Close()
		t.Fatalf("set the pty window size: %v", err)
	}
	return master, slave
}

// Capture application bytes without kernel output translation. Darwin's
// ONLCR can insert a second CR when its queue fills between CR and LF.
// Disabling OPOST also preserves literal CR, tabs and control bytes; nothing
// is normalized after capture. This applies only to output terminals.
func openOutputPty(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, slave := openPty(t)
	words, err := tty.Termios(int(slave.Fd()))
	if err == nil {
		// OPOST is bit 0 on both supported PTY platforms, Linux and Darwin.
		words[1] &^= 1
		err = tty.SetTermios(int(slave.Fd()), 0, words)
	}
	if err != nil {
		master.Close()
		slave.Close()
		t.Fatalf("disable terminal output translation: %v", err)
	}
	return master, slave
}

func TestPtyOutputPreservesBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"controls", []byte("literal\r\n\t\x00\x04\x1b\b\xff\n")},
		{"queue-boundaries", bytes.Repeat([]byte("x\n"), 65536)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, slave := openOutputPty(t)
			defer master.Close()
			defer slave.Close()
			if !tty.IsTerminal(int(slave.Fd())) {
				t.Fatal("capture descriptor is not a terminal")
			}
			rows, cols, err := tty.WindowSize(int(slave.Fd()))
			if err != nil || rows != ptyRows || cols != ptyCols {
				t.Fatalf("terminal dimensions: %dx%d, %v", rows, cols, err)
			}
			reader := drainPty(master)
			if _, err := io.Copy(slave, bytes.NewReader(tc.data)); err != nil {
				t.Fatal(err)
			}
			if got := reader.finish(slave); !bytes.Equal(got, tc.data) {
				t.Fatalf("terminal changed output: wrote %d bytes, captured %d", len(tc.data), len(got))
			}
		})
	}
}

// ptySettings renders everything a run can have LEFT on a terminal: the four
// flag words, the control characters, Linux's line discipline or Darwin's two
// speeds, and the window size. It is the `artifacts` of a utility whose
// output is a terminal — without it a `stty -echo` case compares two empty
// streams and proves nothing about the echo.
//
// It is read through a slave the parent kept open, because a Darwin master
// answers no TIOCGETA.
func ptySettings(t *testing.T, slave *os.File) string {
	t.Helper()
	fd := int(slave.Fd())
	words, err := tty.Termios(fd)
	if err != nil {
		t.Fatalf("read the terminal settings back: %v", err)
	}
	rows, cols, err := tty.WindowSize(fd)
	if err != nil {
		t.Fatalf("read the terminal size back: %v", err)
	}
	out := ""
	for i := 0; i < 4; i++ {
		out += fmt.Sprintf("%x:", uint64(words[i]))
	}
	var cc []int64
	if runtime.GOOS == "darwin" {
		out += fmt.Sprintf("speed=%d/%d:", uint64(words[24]), uint64(words[25]))
		cc = words[4:24]
	} else {
		out += fmt.Sprintf("line=%d:", words[4])
		cc = words[5:]
	}
	for _, c := range cc {
		out += fmt.Sprintf("%x,", c)
	}
	return out + fmt.Sprintf(" %dx%d", rows, cols)
}

// ptyPrepare runs `bin` on the terminal `slave` is open on, to put it in the
// state a case starts from. The REFERENCE binary is what runs: the starting
// state is a premise of the case rather than part of what it proves, and both
// sides have to begin from the same one.
func ptyPrepare(t *testing.T, bin, argv0 string, slave *os.File, args []string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Args = append([]string{argv0}, args...)
	cmd.Env = baseEnv()
	cmd.Stdin = slave
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare the terminal with %s %s: %v\n%s", argv0, quoteArgs(args), err, out)
	}
	if len(out) != 0 {
		t.Fatalf("prepare the terminal with %s %s: wrote %q, which means the case's premise is not one",
			argv0, quoteArgs(args), out)
	}
}

// ptyReader drains a master end into a buffer on its own goroutine.
//
// The drain has to be concurrent: a terminal buffers only a few kilobytes,
// so a child writing more than that into a master nobody is reading blocks
// for ever. `finish` returns what arrived, once the child has exited.
type ptyReader struct {
	done chan struct{}
	buf  []byte
	mu   sync.Mutex
}

// drainPty starts the goroutine. Reading a master whose last slave has closed
// answers EIO on Linux rather than EOF, so any error ends the drain. That EIO
// can arrive with the child's last writes still queued between the two ends,
// which is why the end of the output is marked by finish rather than read off
// the slave's close (#9807).
func drainPty(master *os.File) *ptyReader {
	r := &ptyReader{done: make(chan struct{})}
	go func() {
		defer close(r.done)
		chunk := make([]byte, 4096)
		for {
			n, err := master.Read(chunk)
			if n > 0 {
				r.mu.Lock()
				r.buf = append(r.buf, chunk[:n]...)
				r.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return r
}

// An input terminal can echo even though the child's stdout is a separate
// pipe. Consume that echo so tcsetattr(TCSADRAIN) can finish. The caller
// closes its slave copies before calling the returned cleanup function.
// Echo is not stdout or stderr and must not enter either captured stream.
func discardPtyEcho(master *os.File) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, master)
	}()
	return func() {
		master.Close()
		<-done
	}
}

// ptyEndMark is written into a slave once its child has exited. It queues
// behind everything the child wrote, so reading it back means the output is
// all in. No output processing a case's stty can switch on rewrites it: it
// holds no letter, no newline or return, and no tab or other delayed control.
const ptyEndMark = "\x1f#9807#\x1f"

// ptyEndWait bounds the wait for the mark, for a terminal whose output a case
// left stopped.
const ptyEndWait = 10 * time.Second

// finish ends the capture of a child that has exited: marks the end through
// the parent's own copy of the slave, reads up to the mark, then closes that
// copy and reads to the end, so a descendant still holding the slave is
// captured as before. Returns the output with the mark taken out.
func (r *ptyReader) finish(slave *os.File) []byte {
	go func() { _, _ = slave.Write([]byte(ptyEndMark)) }()
	deadline := time.Now().Add(ptyEndWait)
	for !r.holds(ptyEndMark) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	slave.Close()
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Replace(r.buf, []byte(ptyEndMark), nil, 1)
}

func (r *ptyReader) holds(mark string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Contains(r.buf, []byte(mark))
}

// feedPty writes `text` into a master and follows it with ^D, the terminal's
// end-of-file character, so a child reading fd 0 sees EOF rather than
// waiting for a line that never comes. Empty text is an immediate EOF.
//
// ^D only ENDS THE INPUT at the start of a line — mid-line it just hands the
// partial line over — so a case whose stdin a utility actually reads has to
// end it with a newline, or the reader gets the bytes and no EOF. Nothing
// needs that yet: the terminal cases so far are about the answer to isatty,
// and their stdin is empty.
//
// On its own goroutine for the same reason the drain is: the write blocks
// once the terminal's buffer fills.
func feedPty(master *os.File, text string) {
	go func() {
		if len(text) > 0 {
			_, _ = io.WriteString(master, text)
		}
		_, _ = master.Write([]byte{4})
	}()
}
