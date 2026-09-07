package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// open_exclusive is O_WRONLY|O_CREAT|O_EXCL: the first open creates the
// file, and a second open of the same name fails with EEXIST — which
// reaches the program as `IoError::AlreadyExists(path)` rather than as a
// truncated file. The distinction is the whole point of the primitive
// (#8776): a caller retrying with a fresh random name has to tell EEXIST
// from a real failure.
//
// Both WASI ABIs are driven, because they carry separate bodies:
// preview-1 puts EXCLUSIVE in path_open's `oflags` (0x04, the bit
// between CREATE and TRUNCATE), preview-2 in descriptor.open-at's
// `open-flags` (bit 2, the same layout).
const openExclusiveProg = `function main(): i32 {
    match (open_exclusive("ex.txt")) {
        Ok(w) => {
            match (w.write("new")) { Some(_) => { return 1; }, None => {} }
            match (w.close()) { Some(_) => { return 2; }, None => {} }
        },
        Err(_) => { return 3; }
    }
    match (open_exclusive("ex.txt")) {
        Ok(_) => { return 4; },
        Err(e) => {
            match (e) {
                AlreadyExists(p) => { write("exists:" + p); },
                _ => { return 5; }
            }
        }
    }
    match (read_file("ex.txt")) {
        Ok(s) => { write(":" + s); return 0; },
        Err(_) => { return 6; }
    }
    return 0 - 1;
}`

const openExclusiveWant = "exists:ex.txt:new"

func TestWasmOpenExclusivePreview2(t *testing.T) {
	comp := buildComponent(t, openExclusiveProg)
	dir := t.TempDir()
	stdout, stderr, ec := runComponent(t, comp, runOpts{workDir: dir})
	if ec != 0 || !strings.Contains(stdout, openExclusiveWant) {
		t.Errorf("exit %d, stdout %q (want it to contain %q)\nstderr:\n%s", ec, stdout, openExclusiveWant, stderr)
	}
	got, err := os.ReadFile(filepath.Join(dir, "ex.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("ex.txt = %q, want %q — the refused second open truncated it", got, "new")
	}
}

func TestWasmOpenExclusivePreview1(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(openExclusiveProg), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	wasmPath := filepath.Join(dir, "main.wasm")
	fern := e2eharness.BuildLangBinForInterp(t)
	if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-o", wasmPath, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	out, err := exec.Command("wasmtime", "run", "--dir", dir, wasmPath).CombinedOutput()
	if err != nil || !strings.Contains(string(out), openExclusiveWant) {
		t.Fatalf("wasmtime: %v\noutput %q (want it to contain %q)", err, out, openExclusiveWant)
	}
	got, err := os.ReadFile(filepath.Join(dir, "ex.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("ex.txt = %q, want %q — the refused second open truncated it", got, "new")
	}
}
