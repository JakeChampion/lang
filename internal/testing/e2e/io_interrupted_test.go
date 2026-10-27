//go:build linux

package e2e

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A write-all that an interrupting signal ends after its first byte continues
// to the end, so no byte is lost. One that the signal ends before its first
// byte answers Interrupted, so a caller that sees it knows nothing was written.
const interruptedWriteAllSource = `import "std/i32";
function block(n: i32): string {
    let s: string = "";
    let i: i32 = 0;
    while (i < n) { s = s + "abcdefghij"; i = i + 1; }
    return s;
}
function main(): i32 {
    let usr1: i32 = 10;
    if (signal_catch_interrupting(usr1) != 0) { return 10; }
    let w: Writer = stdout();
    print("ready");
    match (w.write(block(100000))) {
        Ok(_) => { eprint("written"); },
        Err(e) => {
            match (e) {
                Interrupted => { eprint("interrupted"); return 3; },
                _ => { eprint("other"); return 4; }
            }
        }
    }
    if (!signal_taken(usr1)) { return 5; }
    return 0;
}
`

const interruptedWriteNothingSource = `import "std/i32";
function block(n: i32): string {
    let s: string = "";
    let i: i32 = 0;
    while (i < n) { s = s + "0123456789abcdef"; i = i + 1; }
    return s;
}
function main(): i32 {
    let usr1: i32 = 10;
    if (signal_catch_interrupting(usr1) != 0) { return 10; }
    let w: Writer = stdout();
    match (w.write_some(block(256))) {
        Ok(n) => { if (n != 4096 as i64) { eprint("short fill"); return 11; } },
        Err(e) => { eprint("fill failed"); return 12; }
    }
    match (w.write("x")) {
        Ok(_) => { eprint("written"); return 3; },
        Err(e) => {
            match (e) {
                Interrupted => { eprint("interrupted"); },
                _ => { eprint("other"); return 4; }
            }
        }
    }
    if (!signal_taken(usr1)) { return 5; }
    return 0;
}
`

// A read-all that an interrupting signal ends after its first byte continues
// to the end of the file.
const interruptedReadAllSource = `import "std/i32";
function main(): i32 {
    let usr1: i32 = 10;
    if (signal_catch_interrupting(usr1) != 0) { return 10; }
    print("ready");
    match (read_file(args()[1])) {
        Ok(s) => { print(s.len().to_string()); },
        Err(e) => {
            match (e) {
                Interrupted => { eprint("interrupted"); return 3; },
                _ => { eprint("other"); return 4; }
            }
        }
    }
    if (!signal_taken(usr1)) { return 5; }
    return 0;
}
`

// A write that answers zero for a non-empty buffer is an error, never a
// silent success.
const zeroWriteSource = `function main(): i32 {
    let w: Writer = stdout();
    match (w.write("hello")) {
        Ok(_) => { return 7; },
        Err(e) => {
            match (e) {
                Other(p, m, n) => { if (n == 5) { return 0; } return 8; },
                _ => { return 9; }
            }
        }
    }
}
`

func interruptedIOTargets(t *testing.T) []string {
	t.Helper()
	switch runtime.GOARCH {
	case "amd64", "arm64":
	default:
		t.Skip("interrupted I/O tests require an x86-64 or ARM64 Linux host")
	}
	targets := []string{e2eharness.HostSelfHostTarget()}
	if runtime.GOARCH == "amd64" {
		targets = append(targets, "arm64-linux")
	}
	return targets
}

func interruptedIORunner(t *testing.T, target string) []string {
	t.Helper()
	if target != "arm64-linux" || runtime.GOARCH == "arm64" {
		return nil
	}
	if qemu := e2eharness.Arm64Runner(t); qemu != "" {
		return []string{qemu}
	}
	t.Skip("no arm64 runner")
	return nil
}

func setPipeSize(t *testing.T, f *os.File, size int) {
	t.Helper()
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_SETPIPE_SZ, uintptr(size)); errno != 0 {
		t.Fatalf("F_SETPIPE_SZ: %v", errno)
	}
}

// signalUntil sends SIGUSR1 to the process every 100 ms until stop closes.
func signalUntil(p *os.Process, stop <-chan struct{}) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			p.Signal(syscall.SIGUSR1)
		}
	}
}

func TestSelfHostWriteAllContinuesAfterInterrupt(t *testing.T) {
	for _, target := range interruptedIOTargets(t) {
		t.Run(target, func(t *testing.T) {
			runner := interruptedIORunner(t, target)
			bin := e2eharness.CompileSelfHostSource(t, target, interruptedWriteAllSource, nil)
			argv := append(append([]string{}, runner...), bin)
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Env = e2eharness.ChildEnv()
			rd, wr, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			setPipeSize(t, rd, 4096)
			cmd.Stdout = wr
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			wr.Close()
			// Leave the pipe unread until the write has filled it and blocked,
			// then interrupt it while it is blocked.
			time.Sleep(300 * time.Millisecond)
			for i := 0; i < 5; i++ {
				cmd.Process.Signal(syscall.SIGUSR1)
				time.Sleep(100 * time.Millisecond)
			}
			out, err := io.ReadAll(rd)
			if err != nil {
				t.Fatal(err)
			}
			cmd.Wait()
			code := cmd.ProcessState.ExitCode()
			want := "ready\n" + strings.Repeat("abcdefghij", 100000)
			if code != 0 || string(out) != want || strings.TrimSpace(stderr.String()) != "written" {
				t.Fatalf("exit=%d, stdout bytes=%d (want %d), stderr=%q", code, len(out), len(want), stderr.String())
			}
		})
	}
}

func TestSelfHostWriteAllInterruptedBeforeFirstByte(t *testing.T) {
	for _, target := range interruptedIOTargets(t) {
		t.Run(target, func(t *testing.T) {
			runner := interruptedIORunner(t, target)
			bin := e2eharness.CompileSelfHostSource(t, target, interruptedWriteNothingSource, nil)
			argv := append(append([]string{}, runner...), bin)
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Env = e2eharness.ChildEnv()
			rd, wr, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			setPipeSize(t, rd, 4096)
			cmd.Stdout = wr
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			wr.Close()
			stop := make(chan struct{})
			go signalUntil(cmd.Process, stop)
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				cmd.Process.Kill()
				t.Fatal("the blocked write did not answer Interrupted within 30 s")
			}
			close(stop)
			out, err := io.ReadAll(rd)
			if err != nil {
				t.Fatal(err)
			}
			code := cmd.ProcessState.ExitCode()
			if code != 0 || len(out) != 4096 || strings.TrimSpace(stderr.String()) != "interrupted" {
				t.Fatalf("exit=%d, stdout bytes=%d (want 4096), stderr=%q", code, len(out), stderr.String())
			}
		})
	}
}

func TestSelfHostReadAllContinuesAfterInterrupt(t *testing.T) {
	for _, target := range interruptedIOTargets(t) {
		t.Run(target, func(t *testing.T) {
			runner := interruptedIORunner(t, target)
			bin := e2eharness.CompileSelfHostSource(t, target, interruptedReadAllSource, nil)
			dir := t.TempDir()
			fifo := filepath.Join(dir, "in.fifo")
			if err := syscall.Mkfifo(fifo, 0o600); err != nil {
				t.Fatal(err)
			}
			argv := append(append([]string{}, runner...), bin, fifo)
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Env = e2eharness.ChildEnv()
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			// Opening for writing blocks until the program opens the FIFO to read.
			w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
			if err != nil {
				cmd.Process.Kill()
				t.Fatal(err)
			}
			first := bytes.Repeat([]byte("a"), 10000)
			if _, err := w.Write(first); err != nil {
				t.Fatal(err)
			}
			// The read now blocks with progress; interrupt it while it waits.
			time.Sleep(300 * time.Millisecond)
			for i := 0; i < 5; i++ {
				cmd.Process.Signal(syscall.SIGUSR1)
				time.Sleep(100 * time.Millisecond)
			}
			if _, err := w.Write(bytes.Repeat([]byte("b"), 10000)); err != nil {
				t.Fatal(err)
			}
			w.Close()
			cmd.Wait()
			code := cmd.ProcessState.ExitCode()
			if code != 0 || stdout.String() != "ready\n20000\n" || stderr.String() != "" {
				t.Fatalf("exit=%d, stdout=%q, stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestSelfHostWriteAllRefusesZeroProgress(t *testing.T) {
	var nr uintptr
	switch runtime.GOARCH {
	case "amd64":
		nr = 1
	case "arm64":
		nr = 64
	default:
		t.Skip("the zero-write probe requires an x86-64 or ARM64 Linux host")
	}
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.HostSelfHostTarget(), zeroWriteSource, nil)
	control := exec.Command(bin)
	control.Env = e2eharness.ChildEnv()
	if out, _ := control.CombinedOutput(); control.ProcessState == nil || control.ProcessState.ExitCode() != 7 || string(out) != "hello" {
		t.Fatalf("positive control: exit=%d out=%q", control.ProcessState.ExitCode(), out)
	}
	// errno 0 makes the filtered write return 0 without running.
	cmd := underSeccompErrno(t, t.TempDir(), 0, []uintptr{nr}, bin)
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 || len(out) != 0 {
		t.Fatalf("zero write: exit=%d out=%q, want exit 0 from the Other(_, _, 5) arm", cmd.ProcessState.ExitCode(), out)
	}
}
