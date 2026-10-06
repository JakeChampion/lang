package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// The first preview-1 coverage of Reader.seek and remove_dir_all. Both were
// uncovered: read_file asks for fd_seek and then only loops fd_read from
// offset 0, and nothing removed a directory, so neither operation had ever run
// on this path.
//
// It does NOT pin the rights bits, and that is worth stating because it is the
// obvious thing to assume it does. Two wrong constants were corrected beside
// truncate — fd_seek read 0x01, which is fd_datasync, and
// path_remove_directory read 0x8000000, which is poll_fd_readwrite. Putting
// either old value back and running this leaves it GREEN, measured, because
// wasmtime does not enforce fs_rights_base on a preopened directory: the
// rights a path_open asks for are advisory there, which is exactly why two
// wrong ones survived unnoticed. Nothing available here makes them
// observable, so a later tidy-up could still put them back. What this test
// buys is that the two operations now execute at all.
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
	mod := buildPreview1Module(t, preview1RightsSource())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see preview1RightsSource)", got)
	}
	// The directory is the half that proves path_remove_directory: main
	// returning 0 already means remove_dir_all answered Ok, but a walk that
	// silently left it would too.
	if _, err := os.Stat(filepath.Join(dir, "d")); !os.IsNotExist(err) {
		t.Fatalf("d survived remove_dir_all: stat err = %v", err)
	}
}
