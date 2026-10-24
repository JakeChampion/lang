package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
    let d: string = "";
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
	build := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (temp_dir + remove_file) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{
		"[method]descriptor.create-directory-at",
		"[method]descriptor.unlink-file-at",
		"[method]descriptor.open-at",
	} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in the composed component", want)
		}
	}
	if strings.Contains(string(printOut), "[method]descriptor.append-via-stream") {
		t.Errorf("composed component declares append-via-stream, which this program never imports")
	}
	if err := exec.Command("wasmtime", "run", "--dir", dir, compPath).Run(); err != nil {
		t.Errorf("temp_dir + remove_file lifecycle: wasmtime run failed (want exit 0): %v", err)
	}
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
	build := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (stat only) failed: %v\n%s", err, out)
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
	build := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (read + append) failed: %v\n%s", err, out)
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

func TestCmdLangComponentReadDirRemoveDirAll(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "rd.fern")
	src := []byte(`function main(): i32 {
    let d: string = "";
    match (temp_dir("rd")) { Err(e) => { return 1; }, Ok(p) => { d = p; } }
    match (write_file(d + "/a.txt", "aaa")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (write_file(d + "/b.txt", "bb")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (read_dir(d)) {
        Err(e) => { return 1; },
        Ok(names) => {
            if (names.len() != 2) { return 1; }
            let seen: i32 = 0;
            let i: i32 = 0;
            while (i < names.len()) {
                if (names[i] == "a.txt") { seen = seen + 1; }
                if (names[i] == "b.txt") { seen = seen + 1; }
                i = i + 1;
            }
            if (seen != 2) { return 1; }
        }
    }
    let mid: string = d + "/mid";
    match (create_dir_all(mid)) { Err(e) => { return 1; }, Ok(_) => {} }
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
	build := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (read_dir + remove_dir_all) failed: %v\n%s", err, out)
	}
	if err := exec.Command("wasmtime", "run", "--dir", dir, compPath).Run(); err != nil {
		t.Errorf("read_dir + remove_dir_all: wasmtime run failed (want exit 0): %v", err)
	}
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

func TestCmdLangComponentStdTestSuiteRuns(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "suite.fern")
	src := []byte(`import "std/test";
function fabs(x: f64): f64 { if (x > 0.0) { return x; } return 0.0 - x; }
function main(): i32 {
    let r: test.TestRunner = test.test_new("wasm component suite");
    r = r.it("abs positive", () => test.assert_eq_f64_near(fabs(3.5), 3.5, 0.001));
    r = r.it("abs negative", () => test.assert_eq_f64_near(fabs(0.0 - 2.25), 2.25, 0.001));
    return r.finish();
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "suite.wasm")
	build := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("a std/test program must build for -target wasm32-wasi: %v\n%s", err, out)
	}
	run := exec.Command("wasmtime", "run", "--dir", dir, compPath)
	got, err := run.Output()
	if err != nil {
		t.Fatalf("a std/test component must run: %v", err)
	}
	interp := exec.Command(buildFernCLI(t), "-interp", srcPath)
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

func TestCmdLangComponentCreateDirAll(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "mkdirp.fern")
	src := []byte(`function main(): i32 {
    match (create_dir_all("vendor/pkg/src")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (write_file("vendor/pkg/src/lib.fern", "hello")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (read_file("vendor/pkg/src/lib.fern")) {
        Err(e) => { return 1; },
        Ok(s) => { if (s != "hello") { return 1; } }
    }
    match (create_dir_all("vendor/pkg/src")) { Err(e) => { return 1; }, Ok(_) => {} }
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "mkdirp.wasm")
	build := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (create_dir_all) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	if !strings.Contains(string(printOut), "[method]descriptor.create-directory-at") {
		t.Errorf("expected create-directory-at in the composed component")
	}
	if strings.Contains(string(printOut), "[method]descriptor.unlink-file-at") {
		t.Errorf("composed component declares unlink-file-at, which this program never imports")
	}
	if err := exec.Command("wasmtime", "run", "--dir", dir, compPath).Run(); err != nil {
		t.Errorf("create_dir_all lifecycle: wasmtime run failed (want exit 0): %v", err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "vendor", "pkg", "src")); err != nil || !fi.IsDir() {
		t.Errorf("vendor/pkg/src is not a directory under the preopen (err = %v)", err)
	}
}
