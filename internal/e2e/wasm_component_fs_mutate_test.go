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
// -target wasm` builds with Preview2WASI and composes a component, so
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
	build := exec.Command("go", "run", "./cmd/fern", "-target", "wasm", "-o", compPath, srcPath)
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
