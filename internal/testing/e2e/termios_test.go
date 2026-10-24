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

const termiosSource = `function main(): i32 {
    match (termios_get(0)) {
        Err(_) => { return 10; },
        Ok(t) => {
            if (t.len() != 24) { return 11; }
            if (t[5] != (3 as i64)) { return 12; }
            let off: i64[] = t.with(3, t[3] & (0 - 1 - 8));
            // Exercise every control byte, including the end of the ioctl
            // buffer that a nested allocator's stack frame can overwrite.
            let i: i32 = 5;
            while (i < 24) {
                off = off.with(i, (i + 40) as i64);
                i = i + 1;
            }
            match (termios_set(0, 1, off)) { Err(_) => { return 13; }, Ok(_) => {} }
            match (termios_get(0)) {
                Err(_) => { return 14; },
                Ok(u) => {
                    if (u[3] != (t[3] - (8 as i64))) { return 15; }
                    let j: i32 = 5;
                    while (j < 24) {
                        if (u[j] != off[j]) { return 21; }
                        j = j + 1;
                    }
                }
            }
            match (termios_set(0, 1, t)) { Err(_) => { return 16; }, Ok(_) => {} }
            match (termios_get(0)) {
                Err(_) => { return 17; },
                Ok(v) => { if (v[3] != t[3]) { return 18; } }
            }
            match (termios_set(0, 1, [1 as i64])) { Ok(_) => { return 19; }, Err(_) => {} }
            match (termios_set(0, 9, t)) { Ok(_) => { return 20; }, Err(_) => {} }
            return 0;
        }
    }
}
`

func termiosOnPty(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("openpty: %v", err)
	}
	defer master.Close()
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
	master.Close()
	<-done
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func termiosCompile(t *testing.T, target string) (dir, bin string) {
	t.Helper()
	fern := buildLangBinForInterp(t)
	dir = t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(termiosSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "prog")
	out, err := exec.Command(fern, "-target", target, "-o", bin, src).CombinedOutput()
	if err != nil {
		t.Fatalf("compile for %s: %v\n%s", target, err, out)
	}
	return dir, bin
}

func TestX86_64Termios(t *testing.T) {
	_, bin := termiosCompile(t, "x86-64-linux")
	if code := termiosOnPty(t, exec.Command(bin)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see termiosSource)", code)
	}
}

func TestArm64Termios(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	_, bin := termiosCompile(t, "arm64-linux")
	if code := termiosOnPty(t, runArm64Bin(qemu, bin)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see termiosSource)", code)
	}
}

func TestInterpTermios(t *testing.T) {
	fern := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(termiosSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := termiosOnPty(t, exec.Command(fern, "-interp", src)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see termiosSource)", code)
	}
}

func TestWASMTermiosRefused(t *testing.T) {
	prog, err := parser.Parse(termiosSource)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s accepted termios; it has no terminal to configure", target)
			continue
		}
		for _, v := range vs {
			if v.Capability != "tty" {
				t.Errorf("%s: %s refused on %q, want tty", target, v.Builtin, v.Capability)
			}
		}
	}
}
