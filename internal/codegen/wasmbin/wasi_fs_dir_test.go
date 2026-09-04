package wasmbin

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// The directory + metadata builtins (#6208): stat, read_dir, temp_dir,
// remove_file, remove_dir_all.
//
// These run under `wasmtime --dir=.` from a temp directory, like the
// read_file / write_file roundtrip tests above them — the e2e
// harness's `--invoke main` has no preopen at all, so no path builtin
// can work there.

// runFsDirProgram builds `src` for preview-1 and runs main under a
// preopen rooted at a fresh temp directory, returning main's result.
func runFsDirProgram(t *testing.T, src string) string {
	t.Helper()
	return runFsDirProgramWithSetup(t, src, nil)
}

// runFsDirProgramWithSetup is runFsDirProgram plus a hook to seed the preopen
// directory before the module runs — for the things a Fern program cannot
// create for itself, symlinks being the one that matters here.
func runFsDirProgramWithSetup(t *testing.T, src string, setup func(dir string)) string {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := Build(prog, info)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	dir := t.TempDir()
	if setup != nil {
		setup(dir)
	}
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--dir=.", "--invoke", "main", p)
	cmd.Dir = dir
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s\nstdout:%s", err, se.String(), so.String())
	}
	return strings.TrimSpace(so.String())
}

// TestFsStatAndRemoveFile — `stat` reports kind and size, and reports
// NotFound rather than a zeroed FileStat for a missing path; then
// `remove_file` unlinks, and removing what is no longer there IS an
// error (the checker's documented contract, matching os.Remove — it
// is remove_dir_all, not remove_file, that ignores a missing target).
func TestFsStatAndRemoveFile(t *testing.T) {
	src := `function main(): i32 {
    match (write_file("probe.txt", "hello")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (stat("probe.txt")) {
        Err(e) => { return 2; },
        Ok(fs) => {
            if (!fs.is_file) { return 3; }
            if (fs.is_dir) { return 4; }
            if (fs.size != 5) { return 5; }
        }
    }
    match (stat("nope")) { Ok(fs) => { return 6; }, Err(e) => {} }
    match (remove_file("probe.txt")) { Err(e) => { return 7; }, Ok(_) => {} }
    match (stat("probe.txt")) { Ok(fs) => { return 8; }, Err(e) => {} }
    match (remove_file("nope")) { Ok(_) => { return 9; }, Err(e) => {} }
    return 42;
}`
	if got := runFsDirProgram(t, src); got != "42" {
		t.Fatalf("got %q, want 42", got)
	}
}

// TestFsLstatDoesNotFollowSymlinks — `lstat` is `stat` with preview-1's
// symlink_follow lookupflag cleared, so path_filestat_get describes the link
// itself and its filetype comes back SYMBOLIC_LINK: neither REGULAR nor
// DIRECTORY, so both FileStat flags are false.
//
// Asserted against `stat` on the same paths, because that is the only
// difference between the two helpers — they share a body and differ in one
// constant, so an lstat that quietly still followed links would agree with stat
// everywhere and look correct on any test that ran it alone.
func TestFsLstatDoesNotFollowSymlinks(t *testing.T) {
	src := `function main(): i32 {
    // A regular file is a regular file to both.
    match (lstat("real.txt")) { Err(e) => { return 1; }, Ok(fs) => { if (!fs.is_file) { return 2; } } }
    // The link to it: lstat sees neither a file nor a directory, stat sees a file.
    match (lstat("link_to_file")) {
        Err(e) => { return 3; },
        Ok(fs) => { if (fs.is_file) { return 4; } if (fs.is_dir) { return 5; } }
    }
    match (stat("link_to_file")) { Err(e) => { return 6; }, Ok(fs) => { if (!fs.is_file) { return 7; } } }
    // The link to a directory, the same way round.
    match (lstat("link_to_dir")) {
        Err(e) => { return 8; },
        Ok(fs) => { if (fs.is_file) { return 9; } if (fs.is_dir) { return 10; } }
    }
    match (stat("link_to_dir")) { Err(e) => { return 11; }, Ok(fs) => { if (!fs.is_dir) { return 12; } } }
    // A real directory is a directory to both.
    match (lstat("realdir")) { Err(e) => { return 13; }, Ok(fs) => { if (!fs.is_dir) { return 14; } } }
    // A missing path errors rather than reporting "neither".
    match (lstat("nope")) { Ok(fs) => { return 15; }, Err(e) => {} }
    return 42;
}`
	setup := func(dir string) {
		if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("hello"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.Mkdir(filepath.Join(dir, "realdir"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.Symlink("real.txt", filepath.Join(dir, "link_to_file")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := os.Symlink("realdir", filepath.Join(dir, "link_to_dir")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	if got := runFsDirProgramWithSetup(t, src, setup); got != "42" {
		t.Fatalf("got %q, want 42", got)
	}
}

// TestFsTempDirReadDirRemoveAll — the three that `std/test` actually
// calls, exercised as one lifecycle: create a unique directory, see it
// as a directory, list exactly the files put in it, then recursively
// remove it.
//
// The read_dir count is the load-bearing assertion: WASI's fd_readdir
// yields "." and ".." where Go's os.ReadDir (which the interpreter
// wraps) does not, so a missing filter shows up here as 4 entries
// rather than 2 and the two backends would disagree on every listing.
//
// The final remove_dir_all is the os.RemoveAll contract — removing a
// directory that is already gone is Ok, not Err. `std/test`'s cleanup
// depends on it: TestRunner.finish scrubs every registered path
// whether or not the test created one.
func TestFsTempDirReadDirRemoveAll(t *testing.T) {
	src := `function main(): i32 {
    var d: string = "";
    match (temp_dir("probe")) { Err(e) => { return 1; }, Ok(p) => { d = p; } }
    match (stat(d)) {
        Err(e) => { return 2; },
        Ok(fs) => { if (!fs.is_dir) { return 3; } }
    }
    match (write_file(d + "/a.txt", "aaa")) { Err(e) => { return 4; }, Ok(_) => {} }
    match (write_file(d + "/b.txt", "bb")) { Err(e) => { return 5; }, Ok(_) => {} }
    match (read_dir(d)) {
        Err(e) => { return 6; },
        Ok(names) => {
            if (names.len() != 2) { return 7; }
            var seen: i32 = 0;
            var i: i32 = 0;
            while (i < names.len()) {
                if (names[i] == "a.txt") { seen = seen + 1; }
                if (names[i] == "b.txt") { seen = seen + 1; }
                i = i + 1;
            }
            if (seen != 2) { return 8; }
        }
    }
    match (remove_dir_all(d)) { Err(e) => { return 9; }, Ok(_) => {} }
    match (stat(d)) { Ok(fs) => { return 10; }, Err(e) => {} }
    match (remove_dir_all(d)) { Err(e) => { return 11; }, Ok(_) => {} }
    return 42;
}`
	if got := runFsDirProgram(t, src); got != "42" {
		t.Fatalf("got %q, want 42", got)
	}
}

// TestFsRemoveDirAllNested — the recursion, which the flat case above
// cannot reach: remove_dir_all must descend rather than fail with
// ENOTEMPTY, and must dispatch on the dirent's kind (unlink for files,
// recurse-then-rmdir for directories).
func TestFsRemoveDirAllNested(t *testing.T) {
	// create_dir_all builds the levels under the temp directory — that
	// is what makes this a tree rather than two siblings, and the single
	// remove_dir_all(d) at the end has to descend two levels to clear
	// it.
	src := `function main(): i32 {
    var d: string = "";
    match (temp_dir("nest")) { Err(e) => { return 1; }, Ok(p) => { d = p; } }
    match (write_file(d + "/top.txt", "t")) { Err(e) => { return 2; }, Ok(_) => {} }
    var mid: string = d + "/mid";
    match (create_dir_all(mid)) { Err(e) => { return 3; }, Ok(_) => {} }
    match (write_file(mid + "/deep.txt", "d")) { Err(e) => { return 4; }, Ok(_) => {} }
    var leaf: string = mid + "/leaf";
    match (create_dir_all(leaf)) { Err(e) => { return 5; }, Ok(_) => {} }
    match (write_file(leaf + "/deepest.txt", "x")) { Err(e) => { return 6; }, Ok(_) => {} }
    // One call clears all three levels and the files at each.
    match (remove_dir_all(d)) { Err(e) => { return 7; }, Ok(_) => {} }
    match (stat(d)) { Ok(fs) => { return 8; }, Err(e) => {} }
    match (stat(mid)) { Ok(fs) => { return 9; }, Err(e) => {} }
    match (stat(leaf)) { Ok(fs) => { return 10; }, Err(e) => {} }
    return 42;
}`
	if got := runFsDirProgram(t, src); got != "42" {
		t.Fatalf("got %q, want 42", got)
	}
}

// TestFsStdTestBuildsForWasm — the issue's actual repro, reduced to
// the part preview-1 can answer.
//
// `TestRunner.finish` walks its cleanup paths through remove_dir_all
// unconditionally, so before this every program merely IMPORTING
// std/test failed to build for wasm with `unknown callee
// "remove_dir_all"` — whether or not the suite touched a file. Build
// success is the whole assertion; running the suite needs the
// preview-2 halves (the `bin/fern -target wasm32-wasi` path), which are the
// follow-up.
func TestFsStdTestBuildsForWasm(t *testing.T) {
	src := `import "std/test";
function fabs(x: f64): f64 { if (x > 0.0) { return x; } return 0.0 - x; }
function main(): i32 {
    var r: test.TestRunner = test.test_new("wasm f64 suite");
    r = r.it("abs positive", () => test.assert_eq_f64_near(fabs(3.5), 3.5, 0.001));
    return r.finish();
}`
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	if _, err := Build(prog, info); err != nil {
		t.Fatalf("Build: %v — a std/test program must build for wasm", err)
	}
}

// TestFsCreateDirAll — `create_dir_all` builds a whole missing chain
// under the preopen, is idempotent over one that already exists, folds
// doubled separators and a trailing slash into the same directory, and
// still reports a genuine failure (a component that is a regular file)
// as Err. WASI takes an explicit path length, so the per-component
// walk is length arithmetic rather than the natives' NUL rewriting —
// this is where that divergence gets pinned.
// temp_dir's prefix is a NAME, not a path (#6329). This backend used
// to join it onto the preopen, so `temp_dir(d + "/inner")` nested a
// directory under `d` — a call the interpreter and the natives both
// refuse. That divergence was the only way to build a tree in the
// language; create_dir_all is now, so the prefix converges on a name.
func TestFsTempDirPrefixIsName(t *testing.T) {
	src := `function main(): i32 {
    match (create_dir_all("esc")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (temp_dir("plain")) { Err(e) => { return 2; }, Ok(p) => {} }
    match (temp_dir("esc/inner")) { Ok(p) => { return 3; }, Err(e) => {} }
    match (read_dir("esc")) {
        Err(e) => { return 4; },
        Ok(names) => { if (names.len() != 0) { return 5; } }
    }
    return 42;
}`
	if got := runFsDirProgram(t, src); got != "42" {
		t.Fatalf("got %q, want 42", got)
	}
}

func TestFsCreateDirAll(t *testing.T) {
	src := `function main(): i32 {
    match (create_dir_all("a/b/c")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (stat("a/b/c")) { Err(e) => { return 2; }, Ok(fs) => { if (!fs.is_dir) { return 3; } } }
    match (stat("a")) { Err(e) => { return 4; }, Ok(fs) => { if (!fs.is_dir) { return 5; } } }
    match (create_dir_all("a/b/c")) { Err(e) => { return 6; }, Ok(_) => {} }
    match (create_dir_all("x//y/")) { Err(e) => { return 7; }, Ok(_) => {} }
    match (stat("x/y")) { Err(e) => { return 8; }, Ok(fs) => { if (!fs.is_dir) { return 9; } } }
    match (write_file("f.txt", "hi")) { Err(e) => { return 10; }, Ok(_) => {} }
    match (create_dir_all("f.txt/inner")) { Ok(_) => { return 11; }, Err(e) => {} }
    match (write_file("a/b/c/deep.txt", "ok")) { Err(e) => { return 12; }, Ok(_) => {} }
    return 42;
}`
	if got := runFsDirProgram(t, src); got != "42" {
		t.Fatalf("got %q, want 42", got)
	}
}
