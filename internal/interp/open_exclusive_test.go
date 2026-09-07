package interp

import (
	"os"
	"path/filepath"
	"testing"
)

// open_exclusive creates or answers AlreadyExists — it never opens a name
// that is already taken, and never truncates it (#8776). The interpreter is
// the oracle the compiled backends are compared against, so it has to give
// EEXIST the same variant they do.
func TestOpenExclusiveRefusesAnExistingName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "probe.txt")
	src := `function main(): i32 {
    match (open_exclusive("` + path + `")) {
        Ok(w) => {
            match (w.write("new")) { Some(e) => { return 1; }, None => {} }
            match (w.close()) { Some(e) => { return 2; }, None => {} }
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
