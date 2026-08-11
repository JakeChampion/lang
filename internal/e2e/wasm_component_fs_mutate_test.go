package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The preview-2 halves of the path-MUTATING filesystem builtins
// (#6208 part 2 step 1): `temp_dir` over
// wasi:filesystem/types.create-directory-at and `remove_file` over
// unlink-file-at.
//
// Why this is a separate gate from the wasmbin-side tests: those run
// `wasmtime --dir=. --invoke main` against a preview-1 CORE module,
// which never touches the component composer at all. `bin/fern
// -target wasm32-wasi` builds with Preview2WASI and composes a component, so
// it exercises four layers the preview-1 tests cannot — the p2 helper
// bodies, ClassifyCore's method detection, the instance type the
// composer builds for that method set, and the canonical lowerings.
// A mismatch in any one of them produces a component that fails to
// instantiate, which is exactly the failure mode #6208 opened on.
//
// Exit code is the signal, and it is deliberately 0-or-not: the
// component's `_lang_run` normalises every non-zero main to 1 (see
// #6211), so a step number would be indistinguishable from any other
// failure. main returns 0 only if every step passed.

// TestCmdLangComponentTempDirRemoveFile runs a create → write → read
// → unlink → verify-gone lifecycle inside a composed component.
//
// The two negative steps are the load-bearing ones. Reading a file
// after remove_file must FAIL — otherwise unlink-file-at was never
// reached and the assertion would pass against a no-op — and removing
// what is already gone must also fail, which is remove_file's
// documented contract (os.Remove's, not os.RemoveAll's) and the same
// answer the interpreter gives.
func TestCmdLangComponentTempDirRemoveFile(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fsmut.fern")
	src := []byte(`function main(): i32 {
    var d: string = "";
    match (temp_dir("probe")) { Err(e) => { return 1; }, Ok(p) => { d = p; } }
    match (write_file(d + "/a.txt", "hello")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (read_file(d + "/a.txt")) {
        Err(e) => { return 1; },
        Ok(s) => { if (s != "hello") { return 1; } }
    }
    match (remove_file(d + "/a.txt")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (read_file(d + "/a.txt")) { Ok(s) => { return 1; }, Err(e) => {} }
    match (remove_file(d + "/a.txt")) { Ok(_) => { return 1; }, Err(e) => {} }
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "fsmut.wasm")
	build := exec.Command("go", "run", "./cmd/fern", "-target", "wasm32-wasi", "-o", compPath, srcPath)
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm (temp_dir + remove_file) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	// The instance type must declare exactly the methods the core
	// imports. The two new ones have to be there...
	for _, want := range []string{
		"[method]descriptor.create-directory-at",
		"[method]descriptor.unlink-file-at",
		"[method]descriptor.open-at",
	} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in the composed component", want)
		}
	}
	// ...and append-via-stream must NOT: this program never appends,
	// and an instance type that declares a method the core does not
	// import makes the component demand a capability it has no use
	// for. That over-declaration is what the old five-way mode could
	// not avoid and the feature set exists to prevent.
	if strings.Contains(string(printOut), "[method]descriptor.append-via-stream") {
		t.Errorf("composed component declares append-via-stream, which this program never imports")
	}
	if err := exec.Command("wasmtime", "run", "--dir", dir, compPath).Run(); err != nil {
		t.Errorf("temp_dir + remove_file lifecycle: wasmtime run failed (want exit 0): %v", err)
	}
	// temp_dir names the directory after the prefix, and created it
	// under the preopen rather than reporting $TMPDIR — so the
	// evidence it really ran is a `probe-<8 hex>` directory here.
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	found := false
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), "probe-") {
			found = true
			if len(e.Name()) != len("probe-")+8 {
				t.Errorf("temp_dir made %q, want a %d-char name (prefix + '-' + 8 hex)", e.Name(), len("probe-")+8)
			}
		}
	}
	if !found {
		t.Errorf("no probe-* directory under the preopen — create-directory-at did not run")
	}
}

// TestCmdLangComponentStatOnly runs `stat` — and NOTHING else — through
// a composed component.
//
// "Nothing else" is the point twice over. It proves `open-at` is not a
// prerequisite: stat-at takes the preopen descriptor and a path
// directly, so a program that never opens a file for streaming must
// neither import open-at nor declare it. While open-at was
// unconditional this program did not build at all, and every
// filesystem component demanded a capability its core might never use.
//
// It is also the shape that exposed the helper-closure bug: with no
// read/write/list in the program, nothing dragged `__fern_alloc_rc1`
// in behind `__fern_str_copy` (see wasmbin's helper_closure_test.go).
func TestCmdLangComponentStatOnly(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write probe file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	srcPath := filepath.Join(dir, "st.fern")
	src := []byte(`function main(): i32 {
    match (stat("s.txt")) {
        Err(e) => { return 1; },
        Ok(fs) => {
            if (!fs.is_file) { return 1; }
            if (fs.is_dir) { return 1; }
            if (fs.size != 5) { return 1; }
        }
    }
    match (stat("sub")) {
        Err(e) => { return 1; },
        Ok(fs) => { if (fs.is_file) { return 1; } if (!fs.is_dir) { return 1; } }
    }
    match (stat("nope")) { Ok(fs) => { return 1; }, Err(e) => {} }
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "st.wasm")
	build := exec.Command("go", "run", "./cmd/fern", "-target", "wasm32-wasi", "-o", compPath, srcPath)
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm (stat only) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	if !strings.Contains(string(printOut), "[method]descriptor.stat-at") {
		t.Errorf("composed component does not declare stat-at")
	}
	if strings.Contains(string(printOut), "[method]descriptor.open-at") {
		t.Errorf("composed component declares open-at, which a stat-only program never imports")
	}
	// The full descriptor-stat record has to be declared even though
	// Fern's FileStat surfaces two of its six fields — a record missing
	// its timestamps is a different type and the component would fail
	// to instantiate. `datetime` is declared inline rather than
	// outer-aliased from wasi:clocks/wall-clock, so a stat program does
	// not import a clock it never reads.
	if !strings.Contains(string(printOut), "descriptor-stat") {
		t.Errorf("composed component does not declare the descriptor-stat record")
	}
	if strings.Contains(string(printOut), "wasi:clocks/wall-clock") {
		t.Errorf("stat-only component imports wasi:clocks/wall-clock; datetime should be declared inline")
	}
	if err := exec.Command("wasmtime", "run", "--dir", dir, compPath).Run(); err != nil {
		t.Errorf("stat: wasmtime run failed (want exit 0): %v", err)
	}
}

// TestCmdLangComponentReadAppend covers the combination the five-way
// filesystem mode could not express: a program that both READS a file
// and APPENDS to one.
//
// There were bodies for read, write, append and read+write, but not
// read+append — so this program matched nothing and the driver
// rejected it. Two tests asserted that rejection as the expected
// behaviour (`TestCmdLangComponentWrapRejectsImports` and
// `TestCmdLangTargetWasmRejectsUnsupported`, which both named this
// exact source). Under FsFeatures there is no combination left to miss,
// so those two moved to a real remaining gap and this took over as the
// positive case.
//
// The appended content is the assertion: reading back "one\ntwo\n"
// proves append-via-stream positioned at EOF rather than truncating,
// while read-via-stream still worked in the same component.
func TestCmdLangComponentReadAppend(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "ra.fern")
	src := []byte(`function main(): i32 {
    match (write_file("ra.txt", "one\n")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (open_appender("ra.txt")) {
        Err(e) => { return 1; },
        Ok(w) => { w.write("two\n"); w.close(); }
    }
    match (read_file("ra.txt")) {
        Err(e) => { return 1; },
        Ok(s) => { if (s != "one\ntwo\n") { return 1; } }
    }
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "ra.wasm")
	build := exec.Command("go", "run", "./cmd/fern", "-target", "wasm32-wasi", "-o", compPath, srcPath)
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm (read + append) failed: %v\n%s", err, out)
	}
	if err := exec.Command("wasmtime", "run", "--dir", dir, compPath).Run(); err != nil {
		t.Errorf("read + append: wasmtime run failed (want exit 0): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "ra.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "one\ntwo\n" {
		t.Errorf("file content = %q, want %q — append-via-stream did not position at EOF", got, "one\ntwo\n")
	}
}

// TestCmdLangComponentReadDirRemoveDirAll closes the loop on the
// listing surface: `read_dir` over the `directory-entry-stream` cursor,
// and `remove_dir_all` recursing through it.
//
// The entry COUNT is the load-bearing assertion. preview-1's fd_readdir
// yields "." and ".." and the preview-1 body filters them out;
// wasi-filesystem specifies that read-directory omits them, so the
// preview-2 body has no filter at all. If that reading of the spec were
// wrong this reports 4 entries instead of 2, and the two backends would
// disagree on every listing.
//
// The nesting is what the flat case cannot reach — remove_dir_all must
// descend rather than fail with "not empty", and must dispatch on the
// entry's descriptor-type (6 = regular file → unlink, 3 = directory →
// recurse then remove-directory-at).
func TestCmdLangComponentReadDirRemoveDirAll(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "rd.fern")
	// temp_dir's prefix is a preopen-relative PATH here, so passing one
	// that already contains a directory puts the new level underneath
	// it — that is what makes this a tree rather than siblings.
	src := []byte(`function main(): i32 {
    var d: string = "";
    match (temp_dir("rd")) { Err(e) => { return 1; }, Ok(p) => { d = p; } }
    match (write_file(d + "/a.txt", "aaa")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (write_file(d + "/b.txt", "bb")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (read_dir(d)) {
        Err(e) => { return 1; },
        Ok(names) => {
            if (names.len() != 2) { return 1; }
            var seen: i32 = 0;
            var i: i32 = 0;
            while (i < names.len()) {
                if (names[i] == "a.txt") { seen = seen + 1; }
                if (names[i] == "b.txt") { seen = seen + 1; }
                i = i + 1;
            }
            if (seen != 2) { return 1; }
        }
    }
    var mid: string = "";
    match (temp_dir(d + "/mid")) { Err(e) => { return 1; }, Ok(p) => { mid = p; } }
    match (write_file(mid + "/deep.txt", "d")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (read_dir(mid)) {
        Err(e) => { return 1; },
        Ok(names) => { if (names.len() != 1) { return 1; } }
    }
    match (remove_dir_all(d)) { Err(e) => { return 1; }, Ok(_) => {} }
    match (stat(d)) { Ok(fs) => { return 1; }, Err(e) => {} }
    match (remove_dir_all(d)) { Err(e) => { return 1; }, Ok(_) => {} }
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "rd.wasm")
	build := exec.Command("go", "run", "./cmd/fern", "-target", "wasm32-wasi", "-o", compPath, srcPath)
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm (read_dir + remove_dir_all) failed: %v\n%s", err, out)
	}
	if err := exec.Command("wasmtime", "run", "--dir", dir, compPath).Run(); err != nil {
		t.Errorf("read_dir + remove_dir_all: wasmtime run failed (want exit 0): %v", err)
	}
	// The recursion really removed the tree — nothing but the sources
	// should be left under the preopen.
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range ents {
		if e.IsDir() {
			t.Errorf("remove_dir_all left %q behind", e.Name())
		}
	}
}

// TestCmdLangComponentStdTestSuiteRuns is #6208's actual success
// criterion, and the thing the issue was opened about.
//
// `TestRunner.finish` walks its cleanup paths through remove_dir_all
// unconditionally, so before part 1 every program merely IMPORTING
// std/test failed to build for wasm with `unknown callee
// "remove_dir_all"` — whether or not the suite touched a file. Part 1
// made it build for preview-1; this makes it BUILD AND RUN as a real
// component, which is the end of the chain.
//
// Comparing the TAP output against the interpreter rather than just
// checking the exit code is the point: a suite that silently ran zero
// assertions would also exit 0.
func TestCmdLangComponentStdTestSuiteRuns(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "suite.fern")
	src := []byte(`import "std/test";
function fabs(x: f64): f64 { if (x > 0.0) { return x; } return 0.0 - x; }
function main(): i32 {
    var r: test.TestRunner = test.test_new("wasm component suite");
    r = r.it("abs positive", () => test.assert_eq_f64_near(fabs(3.5), 3.5, 0.001));
    r = r.it("abs negative", () => test.assert_eq_f64_near(fabs(0.0 - 2.25), 2.25, 0.001));
    return r.finish();
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "suite.wasm")
	build := exec.Command("go", "run", "./cmd/fern", "-target", "wasm32-wasi", "-o", compPath, srcPath)
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("a std/test program must build for -target wasm: %v\n%s", err, out)
	}
	run := exec.Command("wasmtime", "run", "--dir", dir, compPath)
	got, err := run.Output()
	if err != nil {
		t.Fatalf("a std/test component must run: %v", err)
	}
	interp := exec.Command("go", "run", "./cmd/fern", "-interp", srcPath)
	interp.Dir = projectRoot(t)
	want, err := interp.Output()
	if err != nil {
		t.Fatalf("interpreter oracle failed: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("component TAP output differs from the interpreter\n got:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(string(got), "# pass 2") {
		t.Errorf("suite did not report 2 passing tests:\n%s", got)
	}
}
