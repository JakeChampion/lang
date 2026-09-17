package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Reader.stat / Writer.stat are fstat(2) on the handle's fd, and x86_64ssa had
// no emitter for either: coreutils/wc.fern asks a reader for its size and the
// module failed to link on fn___method_Reader_stat (#9559).
//
// Both now share one body with stat(path) — emitStatLikeHelper, parameterised
// on path-versus-fd and on the label prefix — so this covers the projection
// table and the fd path in one go. A pipe is stat'd as well as a file, because
// the two differ in exactly the S_IFMT branch the projection reads, and a body
// that hardcoded is_file would pass on the file alone.
const x86SSAHandleStatSrc = `function main(): i32 {
    match (open_reader("DATA")) {
        Err(_) => { return 30; },
        Ok(r) => {
            match (r.stat()) {
                Err(_) => { return 1; },
                Ok(st) => {
                    if (!st.is_file) { return 2; }
                    if (st.is_dir) { return 3; }
                    if (st.size != 5 as i64) { return 4; }
                }
            }
        }
    }
    match (stdin().stat()) {
        Err(_) => { return 5; },
        Ok(st) => {
            if (st.is_file) { return 6; }
            if (st.is_dir) { return 7; }
        }
    }
    match (open_writer("OUT")) {
        Err(_) => { return 31; },
        Ok(wr) => {
            match (wr.stat()) {
                Err(_) => { return 8; },
                Ok(st) => {
                    if (!st.is_file) { return 9; }
                    if (st.size != 0 as i64) { return 10; }
                }
            }
        }
    }
    return 0;
}
`

func TestX86_64SSAHandleStat(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	data := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(data, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write data: %v", err)
	}
	out := filepath.Join(dir, "out.txt")
	src := filepath.Join(dir, "handlestat.fern")
	body := strings.ReplaceAll(x86SSAHandleStatSrc, "DATA", data)
	body = strings.ReplaceAll(body, "OUT", out)
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	binPath := filepath.Join(dir, "handlestat.bin")
	compile := exec.Command(bin, "-target", "x86-64-linux", "-backend", "ssa", "-o", binPath, src)
	compile.Env = e2eharness.ChildEnv()
	if outB, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("x86-64 -backend ssa build failed: %v\n%s", err, outB)
	}

	run := runX86Bin(qemu, binPath)
	run.Env = e2eharness.ChildEnv()
	// stdin is a pipe, which is what makes the second leg not a regular file.
	stdin, err := run.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	var errBuf strings.Builder
	run.Stderr = &errBuf
	if err := run.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = stdin.Close()
	err = run.Wait()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v\n%s", err, errBuf.String())
	}
	if code != 0 {
		t.Errorf("handle stat program exited %d, want 0 — each code names the check that failed: "+
			"1/5/8 a stat that errored (reader, stdin, writer), 2/3/4 the reader's is_file, is_dir "+
			"and size, 6/7 stdin reported as a file or a directory when it is a pipe, 9/10 the "+
			"writer's is_file and size.\nstderr: %s", code, errBuf.String())
	}
}
