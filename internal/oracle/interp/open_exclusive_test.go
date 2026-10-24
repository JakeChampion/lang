package interp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenExclusiveRefusesAnExistingName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "probe.txt")
	src := `function main(): i32 {
    match (open_exclusive("` + path + `")) {
        Ok(w) => {
            match (w.write("new")) { Err(e) => { return 1; }, Ok(_) => {} }
            match (w.close()) { Err(e) => { return 2; }, Ok(_) => {} }
        },
        Err(e) => { return 3; }
    }
    match (open_exclusive("` + path + `")) {
        Ok(w2) => { return 4; },
        Err(e2) => {
            match (e2) {
                AlreadyExists(p) => { return 0; },
                _ => { return 5; }
            }
        }
    }
    return 9;
}`
	val, _ := runCapture(t, src)
	if val != "0" {
		t.Errorf("open_exclusive = %s, want 0 (4 = the second open succeeded, 5 = EEXIST came back as something other than AlreadyExists)", val)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("file = %q, want %q — the refused open truncated it", got, "new")
	}
}

func TestOpenExclusiveIsNotReadableByGroupOrOther(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "private.txt")
	src := `function main(): i32 {
    match (open_exclusive("` + path + `")) {
        Ok(w) => {
            match (w.close()) { Err(e) => { return 1; }, Ok(_) => {} }
            return 0;
        },
        Err(e) => { return 2; }
    }
    return 9;
}`
	if val, _ := runCapture(t, src); val != "0" {
		t.Fatalf("open_exclusive = %s, want 0", val)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if leaked := fi.Mode().Perm() & 0o077; leaked != 0 {
		t.Errorf("mode = %#o, leaks %#o to group/other — every compiled backend creates 0600 (#8776)", fi.Mode().Perm(), leaked)
	}
}
