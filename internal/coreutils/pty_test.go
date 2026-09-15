package coreutils

import (
	"io"
	"os"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"unsafe"
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
// Linux only. The slave side is reached through TIOCGPTN — the number behind
// /dev/ptmx, opened as /dev/pts/N — which is a Linux ioctl; Darwin needs
// grantpt/unlockpt out of libc, which is not reachable without cgo. That is
// the same line `/dev/full` is on, and the same answer: skip the case rather
// than compare something else.

// The ioctls a pty needs. TIOCSPTLCK unlocks the slave, TIOCGPTN reads its
// number, and TIOCSWINSZ sets the size a utility asks for.
const (
	tioctlSPTLCK = 0x40045431
	tioctlGPTN   = 0x80045430
	tioctlSWINSZ = 0x5414
)

// ptyRows and ptyCols are the window size every tty case runs at. A fresh
// pty carries 0x0, which is not a size any utility can lay out against — it
// falls back to COLUMNS or to 80 — so the case would be measuring the
// fallback rather than the terminal. 24x80 is the conventional default and
// wide enough for several columns.
const (
	ptyRows = 24
	ptyCols = 80
)

// winsize is struct winsize: rows, cols, and two pixel fields nothing here
// sets.
type winsize struct {
	rows, cols, xpixel, ypixel uint16
}

// openPty returns a fresh master/slave pair, sized ptyRows x ptyCols.
//
// The caller closes both. The SLAVE goes to the child and the caller closes
// its own copy once the child has started — while the parent still holds one,
// a read of the master cannot see the end of the child's output.
func openPty(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("a pty case needs TIOCGPTN, which is a Linux ioctl")
	}
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	unlock := int32(0)
	if err := ioctl(master.Fd(), tioctlSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		master.Close()
		t.Fatalf("unlock the pty slave: %v", err)
	}
	var n int32
	if err := ioctl(master.Fd(), tioctlGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		master.Close()
		t.Fatalf("read the pty number: %v", err)
	}
	slave, err := os.OpenFile("/dev/pts/"+itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		t.Fatalf("open the pty slave: %v", err)
	}
	ws := winsize{rows: ptyRows, cols: ptyCols}
	if err := ioctl(slave.Fd(), tioctlSWINSZ, uintptr(unsafe.Pointer(&ws))); err != nil {
		master.Close()
		slave.Close()
		t.Fatalf("set the pty window size: %v", err)
	}
	return master, slave
}

func ioctl(fd, req, arg uintptr) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); errno != 0 {
		return errno
	}
	return nil
}

// ptyReader drains a master end into a buffer on its own goroutine.
//
// The drain has to be concurrent: a terminal buffers only a few kilobytes,
// so a child writing more than that into a master nobody is reading blocks
// for ever. `wait` returns what arrived, once every slave copy is closed.
type ptyReader struct {
	done chan struct{}
	buf  []byte
	mu   sync.Mutex
}

// drainPty starts the goroutine. Reading a master whose last slave has closed
// answers EIO on Linux rather than EOF, so any error ends the drain: what
// arrived before it is the output either way.
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

func (r *ptyReader) wait() []byte {
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf
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
