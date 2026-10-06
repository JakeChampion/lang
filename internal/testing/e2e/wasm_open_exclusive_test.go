package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// openExclusiveRun runs the program as `wasm` under a fresh preopen and checks
// both the report and the file the refused second open left alone.
func openExclusiveRun(t *testing.T, wasm string) {
	t.Helper()
	dir := t.TempDir()
	stdout, stderr, ec := runWasmArtifact(t, wasm, runOpts{workDir: dir})
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

func TestWasmOpenExclusivePreview2(t *testing.T) {
	openExclusiveRun(t, buildCLIComponent(t, openExclusiveProg))
}

func TestWasmOpenExclusivePreview1(t *testing.T) {
	openExclusiveRun(t, buildWasmCore(t, openExclusiveProg))
}
