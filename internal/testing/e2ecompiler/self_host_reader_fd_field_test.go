package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"
)

// A Reader's or Writer's `fd` field reads on the IR path. The self-host
// represents either handle as its bare descriptor, so the field read is the
// value itself — through a parameter, a local and an enum payload binding
// alike. std/serve reads `r.fd` to hand a file body to tcp_sendfile. stdin is
// fd 0 and stdout fd 1, so the program exits 1 * 10 + 0 + 5 = 15.
func TestSelfHostReaderFdField(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	src := `enum Tail { NoTail, FileTail(Reader, i64) }
function fd_of(t: Tail): i32 {
    match (t) {
        FileTail(r, left) => { return r.fd; },
        _ => { return 0 - 1; }
    }
    return 0 - 1;
}
function fd_direct(r: Reader): i32 { return r.fd; }
function main(): i32 {
    let w: Writer = stdout();
    let t: Tail = FileTail(stdin(), 1 as i64);
    return w.fd * 10 + fd_of(t) + fd_direct(stdin()) + 5;
}
`
	asm, progDir := compileSourceModload(t, runner, driverBin, src)
	if !bytes.Contains([]byte(asm), []byte(".Lssa_")) {
		t.Fatal("the program did not route through the IR path")
	}
	bin := buildBin(t, gcc, progDir, "reader_fd_field", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 15 {
		t.Fatalf("exited %d, want 15", code)
	}
}
