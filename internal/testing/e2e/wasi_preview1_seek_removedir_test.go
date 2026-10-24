package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func preview1RightsSource() string {
	return `function main(): i32 {
    match (open_reader("hello.txt")) {
        Err(_) => { return 30; },
        Ok(r) => {
            match (r.seek(1 as i64, 0)) {
                Err(_) => { return 1; },
                Ok(pos) => { if (pos != 1 as i64) { return 2; } }
            }
            match (r.read_chunk(2)) {
                Err(_) => { return 3; },
                Ok(s) => { if (s != "el") { return 4; } }
            }
            match (r.seek(0 - 2 as i64, 2)) {
                Err(_) => { return 5; },
                Ok(pos) => { if (pos != 3 as i64) { return 6; } }
            }
            r.close();
        }
    }
    match (create_dir("d", 493)) {
        Err(_) => { return 7; },
        Ok(_) => {}
    }
    match (remove_dir_all("d")) {
        Err(_) => { return 8; },
        Ok(_) => {}
    }
    return 0;
}`
}

func TestWASMPreview1SeekAndRemoveDir(t *testing.T) {
	mod := buildWasmCore(t, preview1RightsSource())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see preview1RightsSource)", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "d")); !os.IsNotExist(err) {
		t.Fatalf("d survived remove_dir_all: stat err = %v", err)
	}
}
