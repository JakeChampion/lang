// `window_size` end to end on every backend that provides it — the four
// natives. No wasm world has a terminal to measure, so E066 refuses the
// builtin there (capability `tty`) and the last test in this file is that
// refusal rather than a wasm probe.
//
// Each backend reads the kernel's `struct winsize` by hand — three
// hand-written assembly sequences and one Go one — so each gets a REAL
// terminal, of a size the harness set itself, rather than being taken on
// trust. A helper that answered a plausible 80x24 would pass any test that
// only checked the shape of the answer, which is why the size asked for here
// is one no default produces.
//
// The second half is the refusal: a pipe is not a terminal, and ENOTTY has to
// come back as `Err` rather than as a 0x0 a caller cannot tell from a
// measurement. That is the arm `ls` falls back to COLUMNS on.
package e2e

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// The size the harness stamps on the pty. Neither number is a terminal
// default, so an answer that matches cannot have come from a guess.
const (
	wsRows = 13
	wsCols = 57
)

// windowSizeSource asks about fd 1 and reports what it found through the exit
// code: 0 for the expected size, and a distinct code per way of being wrong.
const windowSizeSource = `function main(): i32 {
    match (window_size(1)) {
        Ok(ws) => {
            if (ws.rows != (13 as i64)) { return 21; }
            if (ws.cols != (57 as i64)) { return 22; }
            return 0;
        },
        Err(e) => { return 23; }
    }
}`

// windowSizeNoTtySource is the same question asked of a descriptor that is not
// a terminal. The Err arm is the answer; an Ok of any size is a fiction.
const windowSizeNoTtySource = `function main(): i32 {
    match (window_size(1)) {
        Ok(ws) => { return 24; },
        Err(e) => { return 0; }
    }
}`

// runOnPTY runs `newCmd()`'s command with its stdout wired to a real pty of
// the size above, and returns the exit code.
func runOnPTY(t *testing.T, newCmd func() *exec.Cmd) int {
	t.Helper()
	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("openPTY: %v", err)
	}
	defer master.Close()
	if err := setPTYSize(slave, wsRows, wsCols); err != nil {
		t.Fatalf("set pty size: %v", err)
	}
	// A pty buffer is small enough that an undrained one can block a writer,
	// so the master is drained for as long as the child runs.
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, master)
		close(done)
	}()
	cmd := newCmd()
	cmd.Stdout = slave
	var errb bytes.Buffer
	cmd.Stderr = &errb
	runErr := cmd.Run()
	slave.Close()
	<-done
	if cmd.ProcessState == nil {
		t.Fatalf("run on a pty: %v\nstderr: %s", runErr, errb.String())
	}
	return cmd.ProcessState.ExitCode()
}

// runOnPipe is the same run with stdout redirected, which is what makes the
// question unanswerable.
func runOnPipe(t *testing.T, newCmd func() *exec.Cmd) int {
	t.Helper()
	cmd := newCmd()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("run redirected: %s", out.String())
	}
	return cmd.ProcessState.ExitCode()
}

// assertWindowSize is the pair of runs every backend gets: the size the
// harness set comes back off a terminal, and a redirected descriptor refuses.
func assertWindowSize(t *testing.T, build func(src string) func() *exec.Cmd) {
	t.Helper()
	if code := runOnPTY(t, build(windowSizeSource)); code != 0 {
		t.Errorf("on a %dx%d pty: exit = %d, want 0 (21 = wrong rows, 22 = wrong cols, 23 = Err)",
			wsRows, wsCols, code)
	}
	if code := runOnPipe(t, build(windowSizeNoTtySource)); code != 0 {
		t.Errorf("redirected: exit = %d, want 0 (24 = answered with a size for a pipe)", code)
	}
}

func TestX86_64WindowSize(t *testing.T) {
	assertWindowSize(t, func(src string) func() *exec.Cmd {
		bin, runner := compileX86_64Bin(t, src)
		return func() *exec.Cmd { return runX86_64Bin(runner, bin) }
	})
}

func TestArm64WindowSize(t *testing.T) {
	assertWindowSize(t, func(src string) func() *exec.Cmd {
		bin, qemu := compileArm64Bin(t, src)
		return func() *exec.Cmd { return runArm64Bin(qemu, bin) }
	})
}

// The arm64 SSA-direct backend writes its own helper, in its own frame
// discipline, so it gets the probe rather than being taken on trust.
func TestArm64SSAWindowSize(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	assertWindowSize(t, func(src string) func() *exec.Cmd {
		bin := compileArm64SSA(t, fern, src, os.Environ())
		return func() *exec.Cmd { return runArm64Bin(qemu, bin) }
	})
}

func TestInterpWindowSize(t *testing.T) {
	fern := buildLangBinForInterp(t)
	n := 0
	assertWindowSize(t, func(src string) func() *exec.Cmd {
		n++
		path := filepath.Join(t.TempDir(), fmt.Sprintf("w%d.fern", n))
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		return func() *exec.Cmd { return exec.Command(fern, "-interp", path) }
	})
}

// The Darwin request number is a different constant from Linux's
// (0x40087468 vs 0x5413), and it reaches a different backend arm. Neither of
// the two legs above runs it — both are Linux targets — so the Mach-O build
// gets its own, executed on Apple Silicon and built everywhere.
func TestArm64DarwinWindowSize(t *testing.T) {
	fern := buildFernCLI(t)
	build := func(src, name string) string {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out := filepath.Join(dir, name)
		if o, err := exec.Command(fern, "-target", "arm64-darwin", "-o", out, srcPath).CombinedOutput(); err != nil {
			t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
		}
		if err := os.Chmod(out, 0o755); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		return out
	}
	ttyBin := build(windowSizeSource, "ws_tty")
	pipeBin := build(windowSizeNoTtySource, "ws_pipe")
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("built for arm64-darwin; execution only runs on Apple Silicon")
	}
	if code := runOnPTY(t, func() *exec.Cmd { return exec.Command(ttyBin) }); code != 0 {
		t.Errorf("on a %dx%d pty: exit = %d, want 0 (21 = wrong rows, 22 = wrong cols, 23 = Err)",
			wsRows, wsCols, code)
	}
	if code := runOnPipe(t, func() *exec.Cmd { return exec.Command(pipeBin) }); code != 0 {
		t.Errorf("redirected: exit = %d, want 0 (24 = answered with a size for a pipe)", code)
	}
}

// Neither WASI preview has an ioctl, and wasi:cli's terminal-output resource
// reports no size, so the answer on that target is a named refusal at check
// time rather than a backend stub: no wasi profile grants `tty`, and E066 says
// so with the builtin's name and the call site's position.
func TestWASMWindowSizeRefused(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	srcPath := filepath.Join(dir, "w.fern")
	src := `function main(): i32 {
    match (window_size(1)) { Ok(ws) => { return 0; }, Err(e) => { return 1; } }
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	emit := exec.Command(bin, "-target", "wasm32-wasi", "-o", filepath.Join(dir, "w.wasm"), srcPath)
	var eb bytes.Buffer
	emit.Stderr = &eb
	if err := emit.Run(); err == nil {
		t.Fatalf("expected a refusal for window_size on wasm32-wasi, got success")
	}
	for _, want := range []string{"E066", "window_size", srcPath} {
		if !bytes.Contains(eb.Bytes(), []byte(want)) {
			t.Errorf("refusal missing %q:\n%s", want, eb.String())
		}
	}
}
