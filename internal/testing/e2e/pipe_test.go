// `pipe()` end to end: a Reader and a Writer joined by the kernel, and the
// shape `split --filter` needs — a forked child whose stdin is the read end.
//
// Each exit code names the step that failed. The child leg is what pins the
// close-on-exec contract: the child runs `c=$(cat)`, which only returns once
// every write end is closed. A write end that leaked across the exec would be
// held open by the child itself, and the probe would hang instead of exiting.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// pipeSource is the probe. `forks` selects the child leg, which the
// interpreter cannot run: its proc_fork answers -38.
func pipeSource(forks bool) string {
	child := ""
	if forks {
		child = `
    let q: Pipe = match (pipe()) {
        Ok(q) => q,
        Err(_) => { return 20; },
    };
    let pid: i32 = proc_fork();
    if (pid < 0) {
        return 21;
    }
    if (pid == 0) {
        match (q.r.dup_onto(0)) { None => {}, Some(_) => { exit(22); } }
        proc_exec("/bin/sh", ["-c", "c=$(cat); [ \"$c\" = piped ] && exit 7; exit 9"]);
        exit(23);
    }
    q.r.close();
    match (q.w.write("piped\n")) { None => {}, Some(_) => { return 24; } }
    q.w.close();
    return proc_waitpid(pid);`
	}
	return `function main(): i32 {
    let p: Pipe = match (pipe()) {
        Ok(p) => p,
        Err(_) => { return 10; },
    };
    match (p.w.write("through\n")) { None => {}, Some(_) => { return 11; } }
    p.w.close();
    match (p.r.read_line()) {
        Some(line) => { if (line != "through\n") { return 12; } },
        None => { return 13; },
    }
    match (p.r.read_line()) {
        Some(_) => { return 14; },
        None => {},
    }
    p.r.close();` + child + `
    return 7;
}
`
}

func TestX86_64Pipe(t *testing.T) {
	code, out := compileRunX86_64WithSetup(t, pipeSource(true), nil)
	if code != 7 {
		t.Fatalf("exit = %d, want 7 — the code names the step (see pipeSource)\n%s", code, out)
	}
}

func TestArm64Pipe(t *testing.T) {
	out, code := compileAndRunArm64(t, pipeSource(true))
	if code != 7 {
		t.Fatalf("exit = %d, want 7 — the code names the step (see pipeSource)\n%s", code, out)
	}
}

// Darwin's pipe is the one hand-written half: XNU reports the write end in x1.
// The binary is built on every host and run only on Apple Silicon.
func TestArm64DarwinPipe(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "pipe.fern")
	if err := os.WriteFile(src, []byte(pipeSource(true)), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "pipe")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	cmd := exec.Command(out)
	o, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Fatalf("exit = %d, want 7 — the code names the step (see pipeSource)\n%s", code, o)
	}
}

func TestInterpPipe(t *testing.T) {
	if code := runInterpExit(t, pipeSource(false)); code != 7 {
		t.Fatalf("exit = %d, want 7 — the code names the step (see pipeSource)", code)
	}
}

// No wasm world has a process to hand an end to, so `pipe` is gated on
// `proc` with fork and exec rather than answering Unsupported at run time.
func TestPipeGatedOnProc(t *testing.T) {
	prog, err := parser.Parse(pipeSource(false))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) != 1 || vs[0].Builtin != "pipe" {
			t.Errorf("%s: violations = %+v, want one, for pipe", target, vs)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
		if vs := platforms.Enforce(prog, target); len(vs) != 0 {
			t.Errorf("%s: unexpected violations: %+v", target, vs)
		}
	}
}
