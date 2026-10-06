package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Three filesystem edges where native wasm answered differently from the
// interpreter and native x86-64 (#11117). Each source checks its own answers
// and returns a code naming the first wrong one, so every leg below asserts
// the same thing: main returns 0. Each program creates what it needs under
// the directory it runs in, which is how the wasm legs, whose harnesses only
// preopen an empty directory, run the same source.

// closedHandleSource uses a Reader and a Writer after close(). Every method
// answers an error (bad file descriptor), read_line its None, and a second
// close reports the first. On a preview-2 component close drops the handle's
// stream and descriptor, and a method that reached the host with one trapped
// on the dropped handle instead.
const closedHandleSource = `function main(): i32 {
    match (write_file("closed.txt", "hello\n")) { Ok(_) => {}, Err(_) => { return 1; } }
    match (open_reader("closed.txt")) {
        Err(_) => { return 2; },
        Ok(r) => {
            match (r.close()) { Some(_) => { return 3; }, None => {} }
            match (r.read_line()) { Some(_) => { return 4; }, None => {} }
            match (r.read_chunk(4)) { Ok(_) => { return 5; }, Err(_) => {} }
            match (r.read_chunk_bytes(4)) { Ok(_) => { return 6; }, Err(_) => {} }
            match (r.stat()) { Ok(_) => { return 7; }, Err(_) => {} }
            match (r.seek(0 as i64, 0)) { Ok(_) => { return 8; }, Err(_) => {} }
            match (r.flags()) { Ok(_) => { return 9; }, Err(_) => {} }
            match (r.fsync()) { None => { return 10; }, Some(_) => {} }
            match (r.fdatasync()) { None => { return 11; }, Some(_) => {} }
            match (r.close()) { None => { return 12; }, Some(_) => {} }
        }
    }
    let bs: u8[] = [120 as u8];
    match (open_writer("closed.txt")) {
        Err(_) => { return 20; },
        Ok(w) => {
            match (w.close()) { Some(_) => { return 21; }, None => {} }
            match (w.write("x")) { None => { return 22; }, Some(_) => {} }
            match (w.write_some("x")) { Ok(_) => { return 23; }, Err(_) => {} }
            match (w.write_bytes(bs)) { None => { return 24; }, Some(_) => {} }
            match (w.write_some_bytes(bs)) { Ok(_) => { return 25; }, Err(_) => {} }
            match (w.truncate(0 as i64)) { None => { return 26; }, Some(_) => {} }
            match (w.stat()) { Ok(_) => { return 27; }, Err(_) => {} }
            match (w.seek(0 as i64, 0)) { Ok(_) => { return 28; }, Err(_) => {} }
            match (w.flags()) { Ok(_) => { return 29; }, Err(_) => {} }
            match (w.fsync()) { None => { return 30; }, Some(_) => {} }
            match (w.fdatasync()) { None => { return 31; }, Some(_) => {} }
            match (w.close()) { None => { return 32; }, Some(_) => {} }
        }
    }
    // open_writer truncated the file, and nothing written after close landed.
    match (read_file("closed.txt")) {
        Err(_) => { return 40; },
        Ok(s) => { if (s != "") { return 41; } }
    }
    return 0;
}
`

// removeDirAllFileSource calls remove_dir_all on a plain file, which removes
// it as rm -rf and os.RemoveAll do. Native wasm answered Ok and left the file.
const removeDirAllFileSource = `function main(): i32 {
    match (write_file("plain.txt", "x")) { Ok(_) => {}, Err(_) => { return 1; } }
    match (remove_dir_all("plain.txt")) { Ok(_) => {}, Err(_) => { return 2; } }
    match (stat("plain.txt")) { Ok(_) => { return 3; }, Err(_) => {} }
    match (create_dir_all("d")) { Ok(_) => {}, Err(_) => { return 4; } }
    match (write_file("d/inner.txt", "y")) { Ok(_) => {}, Err(_) => { return 5; } }
    match (remove_dir_all("d/inner.txt")) { Ok(_) => {}, Err(_) => { return 6; } }
    match (stat("d/inner.txt")) { Ok(_) => { return 7; }, Err(_) => {} }
    match (stat("d")) { Ok(st) => { if (!st.is_dir) { return 8; } }, Err(_) => { return 9; } }
    match (remove_dir_all("d")) { Ok(_) => {}, Err(_) => { return 10; } }
    match (stat("d")) { Ok(_) => { return 11; }, Err(_) => {} }
    return 0;
}
`

// failedReadSource reads from stdin, which the legs below open on a
// directory: the read fails (EISDIR on a kernel and on preview 1,
// `last-operation-failed` on preview 2), and read_chunk must report it as
// an error rather than as end of input. Only the first read is asserted,
// because after a failure wasmtime reports its stdin as closed. An empty
// file is the end-of-input case beside it: Ok with nothing in it.
const failedReadSource = `function main(): i32 {
    match (stdin().read_chunk(8)) { Ok(_) => { return 1; }, Err(_) => {} }
    match (write_file("empty.txt", "")) { Ok(_) => {}, Err(_) => { return 2; } }
    match (open_reader("empty.txt")) {
        Err(_) => { return 3; },
        Ok(r) => {
            match (r.read_chunk(8)) { Ok(s) => { if (s != "") { return 4; } }, Err(_) => { return 5; } }
            match (r.read_chunk_bytes(8)) { Ok(b) => { if (b.len() != 0) { return 6; } }, Err(_) => { return 7; } }
        }
    }
    return 0;
}
`

// dirStdin opens an empty directory to serve as a program's stdin.
func dirStdin(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// parityOpts is how a leg runs its program. The zero value is a fresh
// empty directory and an empty stdin.
type parityOpts struct {
	dir   string   // the working directory, which wasm preopens; empty for a fresh one
	stdin *os.File // nil for an empty stdin
	// maxResources, when > 0, caps the preview-2 host's resource table, so
	// a body that leaks a host resource per call runs out of keys.
	maxResources int
}

func (o parityOpts) runDir(t *testing.T) string {
	if o.dir != "" {
		return o.dir
	}
	return t.TempDir()
}

// The legs run src as `o` says and require main to return 0. The
// self-host component reports main through its exit status alone.

func runParityInterp(t *testing.T, src string, o parityOpts) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(buildLangBinForInterp(t), "-interp", p)
	cmd.Dir = o.runDir(t)
	if o.stdin != nil {
		cmd.Stdin = o.stdin
	}
	if out, code := runWithPipes(t, cmd); code != 0 {
		t.Errorf("interpreter: exit = %d, want 0 (the code names the case)\n%s", code, out)
	}
}

func runParityX86_64(t *testing.T, src string, o parityOpts) {
	t.Helper()
	bin, runner := compileX86_64Bin(t, src)
	cmd := runX86_64Bin(runner, bin)
	cmd.Dir = o.runDir(t)
	if o.stdin != nil {
		cmd.Stdin = o.stdin
	}
	if out, code := runWithPipes(t, cmd); code != 0 {
		t.Errorf("x86-64: exit = %d, want 0 (the code names the case)\n%s", code, out)
	}
}

func runParityPreview1(t *testing.T, src string, o parityOpts) {
	t.Helper()
	mod := buildPreview1Module(t, src)
	if code := runPreview1ModuleStdin(t, mod, o.runDir(t), o.stdin); code != 0 {
		t.Errorf("wasm preview 1: main = %d, want 0 (the code names the case)", code)
	}
}

// runParitySelfHostCore runs src as the self-host's preview-1 core module.
// maxResources does not apply: preview 1 has no resource table to cap.
func runParitySelfHostCore(t *testing.T, src string, o parityOpts) {
	t.Helper()
	stdout, stderr, ec := runWasmArtifact(t, buildWasmCore(t, src), runOpts{workDir: o.runDir(t), stdinFile: o.stdin})
	if ec != 0 {
		t.Fatalf("self-host wasm core: wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Errorf("self-host wasm core: main = %d, want 0 (the code names the case)\nstdout:\n%s\nstderr:\n%s", got, stdout, stderr)
	}
}

// runParitySelfHostComponent runs src as the self-host's preview-2 component.
func runParitySelfHostComponent(t *testing.T, src string, o parityOpts) {
	t.Helper()
	stdout, stderr, ec := runCLIComponent(t, src, runOpts{workDir: o.runDir(t), stdinFile: o.stdin, maxResources: o.maxResources})
	if ec != 0 {
		t.Errorf("self-host wasm component: exit %d, want 0\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
}

func TestInterpClosedHandle(t *testing.T) { runParityInterp(t, closedHandleSource, parityOpts{}) }
func TestX86_64ClosedHandle(t *testing.T) { runParityX86_64(t, closedHandleSource, parityOpts{}) }
func TestWASMPreview1ClosedHandle(t *testing.T) {
	runParityPreview1(t, closedHandleSource, parityOpts{})
}

func TestInterpRemoveDirAllPlainFile(t *testing.T) {
	runParityInterp(t, removeDirAllFileSource, parityOpts{})
}
func TestX86_64RemoveDirAllPlainFile(t *testing.T) {
	runParityX86_64(t, removeDirAllFileSource, parityOpts{})
}
func TestWASMPreview1RemoveDirAllPlainFile(t *testing.T) {
	runParityPreview1(t, removeDirAllFileSource, parityOpts{})
}

func TestInterpReadChunkFailure(t *testing.T) {
	runParityInterp(t, failedReadSource, parityOpts{stdin: dirStdin(t)})
}
func TestX86_64ReadChunkFailure(t *testing.T) {
	runParityX86_64(t, failedReadSource, parityOpts{stdin: dirStdin(t)})
}
func TestWASMPreview1ReadChunkFailure(t *testing.T) {
	runParityPreview1(t, failedReadSource, parityOpts{stdin: dirStdin(t)})
}

func TestSelfHostWasmCoreClosedHandle(t *testing.T) {
	runParitySelfHostCore(t, closedHandleSource, parityOpts{})
}
func TestSelfHostWasmClosedHandle(t *testing.T) {
	runParitySelfHostComponent(t, closedHandleSource, parityOpts{})
}
func TestSelfHostWasmCoreRemoveDirAllPlainFile(t *testing.T) {
	runParitySelfHostCore(t, removeDirAllFileSource, parityOpts{})
}
func TestSelfHostWasmRemoveDirAllPlainFile(t *testing.T) {
	runParitySelfHostComponent(t, removeDirAllFileSource, parityOpts{})
}
func TestSelfHostWasmCoreReadChunkFailure(t *testing.T) {
	runParitySelfHostCore(t, failedReadSource, parityOpts{stdin: dirStdin(t)})
}
func TestSelfHostWasmReadChunkFailure(t *testing.T) {
	runParitySelfHostComponent(t, failedReadSource, parityOpts{stdin: dirStdin(t)})
}
