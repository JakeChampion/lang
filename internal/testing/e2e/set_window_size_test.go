package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/tools/tty"
)

const setWindowSizeSource = `function main(): i32 {
    match (window_size(0)) {
        Err(_) => { return 10; },
        Ok(ws) => { if (ws.rows != (24 as i64) || ws.cols != (80 as i64)) { return 11; } }
    }
    match (set_window_size(0, 40, 100)) { Err(_) => { return 12; }, Ok(_) => {} }
    match (window_size(0)) {
        Err(_) => { return 13; },
        Ok(ws) => { if (ws.rows != (40 as i64) || ws.cols != (100 as i64)) { return 14; } }
    }
    match (set_window_size(1, 40, 100)) { Ok(_) => { return 15; }, Err(_) => {} }
    return 0;
}
`

func setWindowSizePty(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("openpty: %v", err)
	}
	defer master.Close()
	if err := tty.SetWindowSizeFull(int(slave.Fd()), 24, 80, 640, 480); err != nil {
		slave.Close()
		t.Fatalf("plant the size: %v", err)
	}
	cmd.Stdin = slave
	if err := cmd.Start(); err != nil {
		slave.Close()
		t.Fatalf("start: %v", err)
	}
	slave.Close()
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := master.Read(buf); err != nil {
				close(done)
				return
			}
		}
	}()
	_ = cmd.Wait()
	<-done
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("did not exit normally")
	}
	rows, cols, xpixel, ypixel, err := tty.WindowSizeFull(int(master.Fd()))
	if err != nil {
		t.Fatalf("read the size back: %v", err)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see setWindowSizeSource)", code)
	}
	if rows != 40 || cols != 100 {
		t.Errorf("terminal is %dx%d, want 40x100", rows, cols)
	}
	if xpixel != 640 || ypixel != 480 {
		t.Errorf("pixel pair is %dx%d, want the planted 640x480 — a one-ioctl "+
			"set_window_size zeroes what it never read", xpixel, ypixel)
	}
}

func setWindowSizeCompile(t *testing.T, target string) string {
	t.Helper()
	fern := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(setWindowSizeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	out, err := exec.Command(fern, "-target", target, "-o", bin, src).CombinedOutput()
	if err != nil {
		t.Fatalf("compile for %s: %v\n%s", target, err, out)
	}
	return bin
}

func TestX86_64SetWindowSize(t *testing.T) {
	bin := setWindowSizeCompile(t, "x86-64-linux")
	setWindowSizePty(t, exec.Command(bin))
}

func TestArm64SetWindowSize(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	if qemu == "" {
		t.Skip("qemu-aarch64 is not on PATH")
	}
	bin := setWindowSizeCompile(t, "arm64-linux")
	setWindowSizePty(t, exec.Command(qemu, bin))
}

func TestInterpSetWindowSize(t *testing.T) {
	fern := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(setWindowSizeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	setWindowSizePty(t, exec.Command(fern, "-interp", src))
}

func TestWASMSetWindowSizeRefused(t *testing.T) {
	prog, err := parser.Parse(setWindowSizeSource)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s accepted set_window_size; it has no terminal to resize", target)
			continue
		}
		for _, v := range vs {
			if v.Capability != "tty" {
				t.Errorf("%s: %s refused on %q, want tty", target, v.Builtin, v.Capability)
			}
		}
	}
}
