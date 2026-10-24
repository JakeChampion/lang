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

const handleTtySource = `function main(): i32 {
    let a: string[] = args();
    if (a.len() < 2) { return 9; }
    match (open_reader_with(a[1], 2)) {
        Err(_) => { return 10; },
        Ok(r) => {
            match (r.window_size()) {
                Err(_) => { return 11; },
                Ok(ws) => { if (ws.rows != (24 as i64) || ws.cols != (80 as i64)) { return 12; } }
            }
            match (r.set_window_size(40, 100)) { Err(_) => { return 13; }, Ok(_) => {} }
            match (r.window_size()) {
                Err(_) => { return 14; },
                Ok(ws) => { if (ws.rows != (40 as i64) || ws.cols != (100 as i64)) { return 15; } }
            }
            match (r.termios_get()) {
                Err(_) => { return 16; },
                Ok(t) => {
                    if (t.len() != 24) { return 17; }
                    if (t[5] != (3 as i64)) { return 18; }
                    let off: i64[] = t.with(3, t[3] & (0 - 1 - 8));
                    match (r.termios_set(1, off)) { Err(_) => { return 19; }, Ok(_) => {} }
                    match (r.termios_get()) {
                        Err(_) => { return 20; },
                        Ok(u) => { if (u[3] != (t[3] - (8 as i64))) { return 21; } }
                    }
                    match (r.termios_set(1, t)) { Err(_) => { return 22; }, Ok(_) => {} }
                    match (r.termios_get()) {
                        Err(_) => { return 23; },
                        Ok(v) => { if (v[3] != t[3]) { return 24; } }
                    }
                    return 0;
                }
            }
        }
    }
    return 25;
}
`

func handleTtyRun(t *testing.T, args ...string) {
	t.Helper()
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("openpty: %v", err)
	}
	defer master.Close()
	name := slave.Name()
	if err := tty.SetWindowSizeFull(int(slave.Fd()), 24, 80, 640, 480); err != nil {
		slave.Close()
		t.Fatalf("plant the size: %v", err)
	}
	slave.Close()
	out, err := exec.Command(args[0], append(args[1:], name)...).CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run: %v\n%s", err, out)
		}
		code = ee.ExitCode()
	}
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see handleTtySource)\n%s", code, out)
	}
	rows, cols, xpixel, ypixel, err := tty.WindowSizeFull(int(master.Fd()))
	if err != nil {
		t.Fatalf("read the size back: %v", err)
	}
	if rows != 40 || cols != 100 {
		t.Errorf("terminal is %dx%d, want 40x100", rows, cols)
	}
	if xpixel != 640 || ypixel != 480 {
		t.Errorf("pixel pair is %dx%d, want the planted 640x480", xpixel, ypixel)
	}
}

func handleTtyCompile(t *testing.T, target string) string {
	t.Helper()
	fern := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(handleTtySource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	out, err := exec.Command(fern, "-target", target, "-o", bin, src).CombinedOutput()
	if err != nil {
		t.Fatalf("compile for %s: %v\n%s", target, err, out)
	}
	return bin
}

func TestX86_64HandleTty(t *testing.T) {
	handleTtyRun(t, handleTtyCompile(t, "x86-64-linux"))
}

func TestArm64HandleTty(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	if qemu == "" {
		t.Skip("qemu-aarch64 is not on PATH")
	}
	handleTtyRun(t, qemu, handleTtyCompile(t, "arm64-linux"))
}

func TestInterpHandleTty(t *testing.T) {
	fern := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(handleTtySource), 0o644); err != nil {
		t.Fatal(err)
	}
	handleTtyRun(t, fern, "-interp", src)
}

func TestWASMHandleTtyIsNotRefusedAtCheckTime(t *testing.T) {
	prog, err := parser.Parse(handleTtySource)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	methods := map[string]bool{
		"window_size": true, "set_window_size": true,
		"termios_get": true, "termios_set": true,
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		for _, v := range platforms.Enforce(prog, target) {
			if methods[v.Builtin] {
				t.Errorf("%s refused %s on %q at check time; a handle method carries "+
					"its own refusal", target, v.Builtin, v.Capability)
			}
		}
	}
}
