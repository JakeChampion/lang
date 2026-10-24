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

const (
	ptyRows = 24
	ptyCols = 80
)

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

func openOutputPty(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, slave := openPty(t)
	words, err := tty.Termios(int(slave.Fd()))
	if err == nil {
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

type ptyReader struct {
	done chan struct{}
	buf  []byte
	mu   sync.Mutex
}

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

const ptyEndMark = "\x1f#9807#\x1f"

const ptyEndWait = 10 * time.Second

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

func feedPty(master *os.File, text string) {
	if text == "" {
		_, _ = master.Write([]byte{4})
		return
	}
	go func() {
		_, _ = io.WriteString(master, text)
		_, _ = master.Write([]byte{4})
	}()
}

func TestEmptyPtyFeedPrecedesSettings(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("XNU clears FLUSHO when terminal input arrives")
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	master, slave := openPty(t)
	stopEcho := discardPtyEcho(master)
	defer func() { slave.Close(); stopEcho() }()
	feedPty(master, "")
	fd := int(slave.Fd())
	words, err := tty.Termios(fd)
	if err != nil {
		t.Fatal(err)
	}
	const darwinFlusho = 0x800000
	words[3] |= darwinFlusho
	if err := tty.SetTermios(fd, 0, words); err != nil {
		t.Fatal(err)
	}
	runtime.Gosched()
	got, err := tty.Termios(fd)
	if err != nil {
		t.Fatal(err)
	}
	if got[3]&darwinFlusho == 0 {
		t.Fatalf("input arriving after feedPty returned cleared FLUSHO: lflag=%x", got[3])
	}
}
