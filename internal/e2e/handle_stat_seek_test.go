package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// handleStatSeekSource drives the Reader / Writer methods that ask the
// handle rather than move bytes: `stat()` (fstat) and `seek(offset,
// whence)` (lseek) on both. Every failure returns its own exit code.
//
// The file holds "hello". The seeks cover all three whence values and
// read back through the moved position, so a body that reported an
// offset without moving the stream — the preview-2 trap, where the
// stream has to be reopened at the target — fails at the read, not
// only at the offset check. stdin and stdout are pipes here (the
// harness wires both), so `seek` on stdin has to answer ESPIPE and
// `stat` on either has to describe something that is not a file.
func handleStatSeekSource(path, out, app string) string {
	return fmt.Sprintf(`function main(): i32 {
    match (open_reader(%[1]q)) {
        Err(_) => { return 30; },
        Ok(r) => {
            match (r.stat()) {
                Err(_) => { return 1; },
                Ok(st) => {
                    if (!st.is_file) { return 2; }
                    if (st.size != 5 as i64) { return 3; }
                }
            }
            match (r.seek(0 - 2 as i64, 2)) {
                Err(_) => { return 4; },
                Ok(pos) => { if (pos != 3 as i64) { return 5; } }
            }
            match (r.read_chunk(10)) {
                Err(_) => { return 6; },
                Ok(s) => { if (s != "lo") { return 7; } }
            }
            match (r.seek(0 as i64, 1)) {
                Err(_) => { return 8; },
                Ok(pos) => { if (pos != 5 as i64) { return 9; } }
            }
            match (r.seek(1 as i64, 0)) {
                Err(_) => { return 10; },
                Ok(pos) => { if (pos != 1 as i64) { return 11; } }
            }
            match (r.read_chunk(2)) {
                Err(_) => { return 12; },
                Ok(s) => { if (s != "el") { return 13; } }
            }
            match (r.seek(0 - 1 as i64, 0)) {
                Ok(_) => { return 14; },
                Err(_) => {}
            }
            r.close();
        }
    }
    match (stdin().seek(0 as i64, 1)) {
        Ok(_) => { return 15; },
        Err(e) => {
            match (e) {
                Other(_, msg) => { if (msg != "Illegal seek") { return 16; } },
                _ => { return 17; }
            }
        }
    }
    match (stdin().stat()) {
        Err(_) => { return 18; },
        Ok(st) => { if (st.is_file) { return 19; } if (st.is_dir) { return 20; } }
    }
    match (stdout().stat()) {
        Err(_) => { return 21; },
        Ok(st) => { if (st.is_file) { return 22; } if (st.is_dir) { return 23; } }
    }
    match (open_writer(%[2]q)) {
        Err(_) => { return 40; },
        Ok(w) => {
            match (w.write("0123456789")) { Some(_) => { return 41; }, None => {} }
            match (w.seek(0 as i64, 1)) {
                Err(_) => { return 42; },
                Ok(pos) => { if (pos != 10 as i64) { return 43; } }
            }
            match (w.seek(4 as i64, 0)) {
                Err(_) => { return 44; },
                Ok(pos) => { if (pos != 4 as i64) { return 45; } }
            }
            match (w.write("ab")) { Some(_) => { return 46; }, None => {} }
            match (w.seek(0 as i64, 1)) {
                Err(_) => { return 47; },
                Ok(pos) => { if (pos != 6 as i64) { return 48; } }
            }
            match (w.seek(0 as i64, 2)) {
                Err(_) => { return 49; },
                Ok(pos) => { if (pos != 10 as i64) { return 50; } }
            }
            match (w.seek(0 - 1 as i64, 0)) {
                Ok(_) => { return 51; },
                Err(_) => {}
            }
            w.close();
        }
    }
    match (read_file(%[2]q)) {
        Err(_) => { return 52; },
        Ok(s) => { if (s != "0123ab6789") { return 53; } }
    }
    // An append Writer's offset is 0 until it writes and the file's end
    // afterwards, and a seek moves the offset without moving the writes:
    // O_APPEND's rule, which the preview-2 body has to emulate.
    match (open_appender(%[3]q)) {
        Err(_) => { return 60; },
        Ok(a) => {
            match (a.seek(0 as i64, 1)) {
                Err(_) => { return 61; },
                Ok(pos) => { if (pos != 0 as i64) { return 62; } }
            }
            match (a.write("XY")) { Some(_) => { return 63; }, None => {} }
            match (a.seek(0 as i64, 1)) {
                Err(_) => { return 64; },
                Ok(pos) => { if (pos != 7 as i64) { return 65; } }
            }
            match (a.seek(1 as i64, 0)) {
                Err(_) => { return 66; },
                Ok(pos) => { if (pos != 1 as i64) { return 67; } }
            }
            match (a.seek(0 as i64, 1)) {
                Err(_) => { return 68; },
                Ok(pos) => { if (pos != 1 as i64) { return 69; } }
            }
            match (a.write("Z")) { Some(_) => { return 70; }, None => {} }
            a.close();
        }
    }
    match (read_file(%[3]q)) {
        Err(_) => { return 71; },
        Ok(s) => { if (s != "helloXYZ") { return 72; } }
    }
    match (stdout().seek(0 as i64, 1)) {
        Ok(_) => { return 73; },
        Err(e) => {
            match (e) {
                Other(_, msg) => { if (msg != "Illegal seek") { return 74; } },
                _ => { return 75; }
            }
        }
    }
    return 0;
}
`, path, out, app)
}

// handleProbeTree is the three paths the source above works over: the
// five-byte file the Reader reads, the name the Writer creates, and a
// five-byte file the appender opens.
func handleProbeTree(t *testing.T) (path, out, app string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	app = filepath.Join(dir, "app.txt")
	if err := os.WriteFile(app, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, filepath.Join(dir, "out.txt"), app
}

// runWithPipes runs cmd with stdin and stdout both pipes — the shape a
// utility meets in a pipeline, and the one under which a seek on stdin
// must fail. The test process's own stdin would otherwise be inherited,
// and under `go test` that is /dev/null, which lseek accepts.
func runWithPipes(t *testing.T, cmd *exec.Cmd) (string, int) {
	t.Helper()
	cmd.Stdin = strings.NewReader("")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("did not exit normally (out=%q)", out.String())
	}
	return out.String(), cmd.ProcessState.ExitCode()
}

func TestX86_64HandleStatSeek(t *testing.T) {
	src := handleStatSeekSource(handleProbeTree(t))
	bin, runner := compileX86_64Bin(t, src)
	out, code := runWithPipes(t, runX86_64Bin(runner, bin))
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see handleStatSeekSource)\n%s", code, out)
	}
}

func TestArm64HandleStatSeek(t *testing.T) {
	src := handleStatSeekSource(handleProbeTree(t))
	bin, qemu := compileArm64Bin(t, src)
	out, code := runWithPipes(t, runArm64Bin(qemu, bin))
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see handleStatSeekSource)\n%s", code, out)
	}
}

// The interpreter answers `stat` and `seek` from the *os.File behind the
// handle, and its stdio streams are the process's own here, so it meets
// the same pipes the compiled backends do.
func TestInterpHandleStatSeek(t *testing.T) {
	src := handleStatSeekSource(handleProbeTree(t))
	p := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runWithPipes(t, exec.Command(buildLangBinForInterp(t), "-interp", p))
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see handleStatSeekSource)\n%s", code, out)
	}
}

// handleStatSeekWasmSource is the preview-2 half. A handle there is a
// stream plus the descriptor it was opened on: `stat` asks the
// descriptor, `seek` reopens the stream at the target and records the
// position, which is what makes SEEK_CUR answerable at all — on the
// Writer too, where every write moves the recorded offset and an append
// Writer's stream is left alone so its writes keep landing at the end
// (wasi_writer_seek.go). stdin and stdout are streams with no
// descriptor, so `seek` on either is ESPIPE exactly as on a kernel, and
// `stat` on either is the all-zero record a preview-1 host reports for
// the same stream: not a file, not a directory, no size.
func handleStatSeekWasmSource() string {
	return `function main(): i32 {
    match (open_reader("hello.txt")) {
        Err(_) => { return 30; },
        Ok(r) => {
            match (r.stat()) {
                Err(_) => { return 1; },
                Ok(st) => {
                    if (!st.is_file) { return 2; }
                    if (st.size != 5 as i64) { return 3; }
                }
            }
            match (r.read_chunk(2)) {
                Err(_) => { return 24; },
                Ok(s) => { if (s != "he") { return 25; } }
            }
            match (r.seek(0 as i64, 1)) {
                Err(_) => { return 26; },
                Ok(pos) => { if (pos != 2 as i64) { return 27; } }
            }
            match (r.seek(0 - 2 as i64, 2)) {
                Err(_) => { return 4; },
                Ok(pos) => { if (pos != 3 as i64) { return 5; } }
            }
            match (r.read_chunk(10)) {
                Err(_) => { return 6; },
                Ok(s) => { if (s != "lo") { return 7; } }
            }
            match (r.seek(0 as i64, 1)) {
                Err(_) => { return 8; },
                Ok(pos) => { if (pos != 5 as i64) { return 9; } }
            }
            match (r.seek(1 as i64, 0)) {
                Err(_) => { return 10; },
                Ok(pos) => { if (pos != 1 as i64) { return 11; } }
            }
            match (r.read_chunk(2)) {
                Err(_) => { return 12; },
                Ok(s) => { if (s != "el") { return 13; } }
            }
            match (r.seek(0 - 1 as i64, 0)) {
                Ok(_) => { return 14; },
                Err(_) => {}
            }
            r.close();
        }
    }
    match (stdin().seek(0 as i64, 1)) {
        Ok(_) => { return 15; },
        Err(e) => {
            match (e) {
                Other(_, msg) => { if (msg != "Illegal seek") { return 16; } },
                _ => { return 17; }
            }
        }
    }
    match (stdin().stat()) {
        Err(_) => { return 18; },
        Ok(st) => { if (st.is_file || st.is_dir || st.size != 0 as i64) { return 19; } }
    }
    match (stdout().stat()) {
        Err(_) => { return 21; },
        Ok(st) => { if (st.is_file || st.is_dir || st.size != 0 as i64) { return 22; } }
    }
    match (open_writer("out.txt")) {
        Err(_) => { return 40; },
        Ok(w) => {
            match (w.write("0123456789")) { Some(_) => { return 41; }, None => {} }
            match (w.seek(0 as i64, 1)) {
                Err(_) => { return 42; },
                Ok(pos) => { if (pos != 10 as i64) { return 43; } }
            }
            match (w.seek(4 as i64, 0)) {
                Err(_) => { return 44; },
                Ok(pos) => { if (pos != 4 as i64) { return 45; } }
            }
            match (w.write("ab")) { Some(_) => { return 46; }, None => {} }
            match (w.seek(0 as i64, 1)) {
                Err(_) => { return 47; },
                Ok(pos) => { if (pos != 6 as i64) { return 48; } }
            }
            match (w.seek(0 as i64, 2)) {
                Err(_) => { return 49; },
                Ok(pos) => { if (pos != 10 as i64) { return 50; } }
            }
            match (w.seek(0 - 1 as i64, 0)) {
                Ok(_) => { return 51; },
                Err(_) => {}
            }
            w.close();
        }
    }
    match (read_file("out.txt")) {
        Err(_) => { return 52; },
        Ok(s) => { if (s != "0123ab6789") { return 53; } }
    }
    match (open_appender("app.txt")) {
        Err(_) => { return 60; },
        Ok(a) => {
            match (a.seek(0 as i64, 1)) {
                Err(_) => { return 61; },
                Ok(pos) => { if (pos != 0 as i64) { return 62; } }
            }
            match (a.write("XY")) { Some(_) => { return 63; }, None => {} }
            match (a.seek(0 as i64, 1)) {
                Err(_) => { return 64; },
                Ok(pos) => { if (pos != 7 as i64) { return 65; } }
            }
            match (a.seek(1 as i64, 0)) {
                Err(_) => { return 66; },
                Ok(pos) => { if (pos != 1 as i64) { return 67; } }
            }
            match (a.seek(0 as i64, 1)) {
                Err(_) => { return 68; },
                Ok(pos) => { if (pos != 1 as i64) { return 69; } }
            }
            match (a.write("Z")) { Some(_) => { return 70; }, None => {} }
            a.close();
        }
    }
    match (read_file("app.txt")) {
        Err(_) => { return 71; },
        Ok(s) => { if (s != "helloXYZ") { return 72; } }
    }
    match (stdout().seek(0 as i64, 1)) {
        Ok(_) => { return 73; },
        Err(e) => {
            match (e) {
                Other(_, msg) => { if (msg != "Illegal seek") { return 74; } },
                _ => { return 75; }
            }
        }
    }
    return 0;
}
`
}

func TestWASMHandleStatSeek(t *testing.T) {
	stdout, stderr, ec, _ := runWasmInDirOpts(t, handleStatSeekWasmSource(),
		map[string]string{"hello.txt": "hello", "app.txt": "hello"}, runOpts{stdin: ""})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Errorf("main = %d, want 0 — the code names the case (see handleStatSeekWasmSource)\nstdout:\n%s\nstderr:\n%s",
			got, stdout, stderr)
	}
}
