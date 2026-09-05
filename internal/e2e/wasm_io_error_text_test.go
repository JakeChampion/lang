package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// IoError.Other's message is strerror's text for the errno on wasm as
// on every other backend (#8265). The conformance corpus cannot reach
// this: its wasm leg mounts no directory, so no path is openable there
// (conformance/cases/io_error_other_*/meta). This mounts one and reads
// through a regular file as if it were a directory — ENOTDIR from the
// host on both WASI framings — and pins the text on each: preview 1
// carries the errno straight through, preview 2 reports a
// wasi:filesystem error-code the runtime translates first
// (__wasi_errno_of_code), which is the path that used to collapse
// every failure to NotFound.
const ioErrorTextProg = `function main(): i32 {
    match (read_file("reg.txt/nested")) {
        Ok(_) => { print("read through a file"); return 1; },
        Err(e) => {
            match (e) {
                Other(p, m) => { print(p + ": " + m); return 0; },
                NotFound(p) => { print("notfound " + p); return 2; },
                _ => { print("wrong variant"); return 3; }
            }
        }
    }
}
`

const ioErrorTextWant = "reg.txt/nested: Not a directory"

func ioErrorTextDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reg.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write reg.txt: %v", err)
	}
	return dir
}

func TestWasmIoErrorOtherTextPreview2(t *testing.T) {
	comp := buildComponent(t, ioErrorTextProg)
	stdout, stderr, ec := runComponent(t, comp, runOpts{workDir: ioErrorTextDir(t)})
	if ec != 0 || !strings.Contains(stdout, ioErrorTextWant) {
		t.Errorf("exit %d, stdout %q (want it to contain %q)\nstderr:\n%s", ec, stdout, ioErrorTextWant, stderr)
	}
}

func TestWasmIoErrorOtherTextPreview1(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := ioErrorTextDir(t)
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(ioErrorTextProg), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	// The preview-1 core `fern -target wasm32-wasi -o` emits, run by
	// wasmtime directly.
	wasmPath := filepath.Join(dir, "main.wasm")
	fern := e2eharness.BuildLangBinForInterp(t)
	if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-o", wasmPath, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	out, err := exec.Command("wasmtime", "run", "--dir", dir, wasmPath).CombinedOutput()
	if err != nil || !strings.Contains(string(out), ioErrorTextWant) {
		t.Errorf("wasmtime: %v\noutput %q (want it to contain %q)", err, out, ioErrorTextWant)
	}
}
