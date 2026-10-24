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

const (
	wsRows = 13
	wsCols = 57
)

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

const windowSizeNoTtySource = `function main(): i32 {
    match (window_size(1)) {
        Ok(ws) => { return 24; },
        Err(e) => { return 0; }
    }
}`

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

func TestWASMWindowSizeRefused(t *testing.T) {
	dir := t.TempDir()
	bin := buildFernCLI(t)
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
