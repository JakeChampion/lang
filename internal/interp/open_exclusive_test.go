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

// The file open_exclusive creates is not readable by group or other.
//
// A caller reaches for this primitive because the CONTENT is not to be
// disclosed — split's spool of piped input is the caller that drove it — so
// the mode is part of the contract, not a detail. Every compiled backend
// passes 0600; the interpreter is the oracle they are diffed against, and it
// passed 0644 until this test existed, which is a divergence the fixpoint is
// structurally unable to see and which the e2e cases miss because they are
// compiled-only.
//
// Asserted as "no group and no other bits" rather than as mode == 0600: a
// umask only ever CLEARS bits, so this property holds under any umask while
// an exact comparison would pin the one the test happens to run under.
func TestOpenExclusiveIsNotReadableByGroupOrOther(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "private.txt")
	src := `function main(): i32 {
    match (open_exclusive("` + path + `")) {
        Ok(w) => {
            match (w.close()) { Some(e) => { return 1; }, None => {} }
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
