// `termios_get(fd)` / `termios_set(fd, when, words)` end to end on every
// backend that provides them: one TCGETS into the kernel's own words, and the
// TCSETS family back out of them.
//
// The words are NOT normalised, which is the whole design (#9356): `stty -g`
// prints them in hex and its restore form reads them back, so a Fern bit
// numbering could not reproduce the output. That makes the layout part of the
// contract, and these cases pin it — the length, which slot each field is in,
// and that a value written comes back.
//
// Every call needs a real terminal: on a pipe they all answer ENOTTY, so the
// probe runs with a pseudo-terminal on fd 0 and a pipe would make it vacuous.
// The cases are chosen so a wrong request number or a mispacked struct cannot
// pass:
//
//   - the length is the target's 24 (four flag words, the line discipline,
//     and NCCS = 19 control characters);
//   - VINTR on a fresh pseudo-terminal is ^C, which says the control
//     characters start where the layout claims and not one slot out;
//   - clearing ECHO moves lflag by exactly that bit and the new value is what
//     the next read reports, so the write reached the kernel;
//   - restoring the original returns it, so nothing else moved;
//   - a short array and an out-of-range action are both refused, so neither
//     is read past nor silently applied.
//
// arm64-ssa has no leg: that backend does not emit these two helpers, and
// says so at link time rather than miscompiling — `docs/BACKEND-PARITY.md`.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
	"github.com/jakechampion/lang/internal/tty"
)

const termiosSource = `function main(): i32 {
    match (termios_get(0)) {
        Err(_) => { return 10; },
        Ok(t) => {
            if (t.len() != 24) { return 11; }
            if (t[5] != (3 as i64)) { return 12; }
            var off: i64[] = t.with(3, t[3] & (0 - 1 - 8));
            // Exercise every control byte, including the end of the ioctl
            // buffer that a nested allocator's stack frame can overwrite.
            var i: i32 = 5;
            while (i < 24) {
                off = off.with(i, (i + 40) as i64);
                i = i + 1;
            }
            match (termios_set(0, 1, off)) { Err(_) => { return 13; }, Ok(_) => {} }
            match (termios_get(0)) {
                Err(_) => { return 14; },
                Ok(u) => {
                    if (u[3] != (t[3] - (8 as i64))) { return 15; }
                    var j: i32 = 5;
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

// termiosOnPty runs the compiled probe with a fresh pseudo-terminal as fd 0,
// which is the only descriptor shape any of its calls can answer. The master
// is drained so a child that wrote more than a terminal holds could not
// deadlock; this one writes nothing.
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
	// The parent's copy goes as soon as the child holds it, or the drain
	// never sees the end of the output.
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

// termiosCompile builds the probe for `target` through the fern CLI and
// returns the binary's path. The in-process helpers beside it all RUN what
// they build, and this test has to hand the child a terminal instead of a
// pipe, so it needs the path.
func termiosCompile(t *testing.T, target, backend string) (dir, bin string) {
	t.Helper()
	fern := buildLangBinForInterp(t)
	dir = t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(termiosSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "prog")
	args := []string{"-target", target, "-o", bin, src}
	if backend != "" {
		args = append([]string{"-backend", backend}, args...)
	}
	out, err := exec.Command(fern, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("compile for %s -backend %q: %v\n%s", target, backend, err, out)
	}
	return dir, bin
}

func TestX86_64Termios(t *testing.T) {
	_, bin := termiosCompile(t, "x86-64-linux", "")
	if code := termiosOnPty(t, exec.Command(bin)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see termiosSource)", code)
	}
}

func TestArm64Termios(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	_, bin := termiosCompile(t, "arm64-linux", "")
	if code := termiosOnPty(t, runArm64Bin(qemu, bin)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see termiosSource)", code)
	}
}

// The interpreter's descriptors are the program's, so this reads the same
// terminal a compiled binary would — through Go's syscall package behind
// internal/tty's per-OS split rather than through emitted assembly.
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

// Both wasm worlds refuse it at check time. Neither preview has a terminal to
// configure — preview 1's fd_fdstat_get reports a filetype and nothing about
// line discipline, and the component model has no terminal interface at all —
// so there is no truthful answer to what the settings of a terminal that
// cannot exist are. That is `window_size`'s case rather than `syncfs`'s,
// which is why it is E066 and not an Unsupported at the call.
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

// TestArm64SSATermios is the same probe under the arm64 SSA backend, which
// emits its own termios_get / termios_set. Named rather than inherited: the
// test above takes the target's default, so whichever emitter that is, the
// other one goes unexercised.
func TestArm64SSATermios(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	_, bin := termiosCompile(t, "arm64-linux", "ssa")
	if code := termiosOnPty(t, runArm64Bin(qemu, bin)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see termiosSource)", code)
	}
}
