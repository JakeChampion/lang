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
	// temp_dir's prefix is a PATH, so passing one that already contains
	// a directory puts the new level underneath it — that is what makes
	// this a tree rather than two siblings, and the single
	// remove_dir_all(d) at the end has to descend two levels to clear
	// it.
	src := `function main(): i32 {
    var d: string = "";
    match (temp_dir("nest")) { Err(e) => { return 1; }, Ok(p) => { d = p; } }
    match (write_file(d + "/top.txt", "t")) { Err(e) => { return 2; }, Ok(_) => {} }
    var mid: string = "";
    match (temp_dir(d + "/mid")) { Err(e) => { return 3; }, Ok(p) => { mid = p; } }
    match (write_file(mid + "/deep.txt", "d")) { Err(e) => { return 4; }, Ok(_) => {} }
    var leaf: string = "";
    match (temp_dir(mid + "/leaf")) { Err(e) => { return 5; }, Ok(p) => { leaf = p; } }
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
// preview-2 halves (the `bin/fern -target wasm` path), which are the
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
