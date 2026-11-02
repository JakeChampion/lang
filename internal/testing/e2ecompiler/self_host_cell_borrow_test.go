package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// quiet reads a cell and uses the read before any write or call, so the
// read borrows the cell's content. written and called put a write or a call
// between the read and its use, so each keeps an owned copy. local reads a
// cell whose last use is the read, so the cell must stay alive until the
// payload is read.
const cellBorrowProgram = `struct Err { msg: string, code: i32 }

@noinline function quiet(c: Cell[Option[Err]]): i32 {
    if let Some(e) = c.get() {
        return e.code;
    }
    return 0 - 1;
}

@noinline function written(c: Cell[Option[Err]]): i32 {
    let o: Option[Err] = c.get();
    c.set(None);
    if let Some(e) = o {
        return e.code + e.msg.len();
    }
    return 0 - 1;
}

@noinline function clear(c: Cell[Option[Err]]): i32 {
    c.set(None);
    return 1;
}

@noinline function called(c: Cell[Option[Err]]): i32 {
    let o: Option[Err] = c.get();
    let k: i32 = clear(c);
    if let Some(e) = o {
        return e.code + k;
    }
    return 0 - k;
}

@noinline function local(code: i32): i32 {
    let c: Cell[Option[Err]] = cell_new(Some(Err { msg: "local", code: code }));
    if let Some(e) = c.get() {
        return e.code + e.msg.len();
    }
    return 0 - 1;
}

function main(): i32 {
    let c: Cell[Option[Err]] = cell_new(Some(Err { msg: "boom", code: 7 }));
    if (quiet(c) != 7) {
        return 1;
    }
    if (written(c) != 11 || written(c) != 0 - 1) {
        return 2;
    }
    c.set(Some(Err { msg: "again", code: 3 }));
    if (called(c) != 4 || called(c) != 0 - 1) {
        return 3;
    }
    if (local(5) != 10 || quiet(c) != 0 - 1) {
        return 4;
    }
    if (__rc_underflow_count() != 0) {
        return 5;
    }
    return 42;
}
`

// certifierDumpOf returns the lowered RC view of name, as the certifier
// lifts it from the emitted ops.
func certifierDumpOf(t *testing.T, h ssaBackendHost, src, name string) string {
	t.Helper()
	cmd := exec.Command(h.cli, "-O", "-target", "x86-64-linux", "-emit", "asm", "-o", filepath.Join(t.TempDir(), "out.s"), src, h.stdlib)
	cmd.Env = append(os.Environ(), "FERN_RC_CERTIFY=report", "FERN_RC_CERTIFY_DUMP="+name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dumping %s: %v\n%s", name, err, out)
	}
	marker := "func " + name + " {\n"
	at := strings.Index(string(out), marker)
	if at < 0 {
		t.Fatalf("no certifier dump of %s:\n%s", name, out)
	}
	body := string(out)[at:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end+3]
	}
	return body
}

func TestSelfHostCellReadBorrows(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "cell_borrow.fern")
	if err := os.WriteFile(src, []byte(cellBorrowProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"quiet", "local"} {
		dump := certifierDumpOf(t, h, src, name)
		if !strings.Contains(dump, "arr_at 1 rc") {
			t.Fatalf("%s does not read its cell as an array slot, so the shape below is not a cell read:\n%s", name, dump)
		}
		if strings.Contains(dump, "__fern_rc_inc") {
			t.Errorf("%s retains the content its cell read borrows:\n%s", name, dump)
		}
	}
	if dump := certifierDumpOf(t, h, src, "quiet"); strings.Contains(dump, "__sem_release_") {
		t.Errorf("quiet releases a copy of its cell read:\n%s", dump)
	}
	for _, name := range []string{"written", "called"} {
		dump := certifierDumpOf(t, h, src, name)
		if !strings.Contains(dump, "__fern_rc_inc") {
			t.Errorf("%s reads its cell before a write or a call replaces the content, so the read must own a copy:\n%s", name, dump)
		}
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 42 {
			t.Errorf("%s: exit %d, want 42 (1: quiet, 2: written, 3: called, 4: local, 5: a count went below zero)", tg.target, got)
		}
	}
}

func TestSelfHostCellReadBorrowsCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "cell_borrow.fern")
	if err := os.WriteFile(src, []byte(cellBorrowProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1"), nil)
	if exit != 42 {
		t.Fatalf("leakcheck: exit = %d, want 42\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
	stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1"), nil)
	if exit != 42 || forArrStructSanitizerFault(stderr, true) {
		t.Fatalf("sanitize: exit = %d, want 42, and no sanitizer report\n%s", exit, stderr)
	}
}
