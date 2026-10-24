package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const openExclusiveProg = `function main(): i32 {
    match (open_exclusive("ex.txt")) {
        Ok(w) => {
            match (w.write("new")) { Err(_) => { return 1; }, Ok(_) => {} }
            match (w.close()) { Err(_) => { return 2; }, Ok(_) => {} }
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
