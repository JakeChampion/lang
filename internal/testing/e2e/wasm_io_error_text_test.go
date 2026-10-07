package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
                Other(p, m, _) => { print(p + ": " + m); return 0; },
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

// ioErrorTextRun runs the program as `wasm` with ioErrorTextDir preopened.
func ioErrorTextRun(t *testing.T, wasm string) {
	t.Helper()
	stdout, stderr, ec := runWasmArtifact(t, wasm, runOpts{workDir: ioErrorTextDir(t)})
	if ec != 0 || !strings.Contains(stdout, ioErrorTextWant) {
		t.Errorf("exit %d, stdout %q (want it to contain %q)\nstderr:\n%s", ec, stdout, ioErrorTextWant, stderr)
	}
}

func TestWasmIoErrorOtherTextPreview2(t *testing.T) {
	ioErrorTextRun(t, buildCLIComponent(t, ioErrorTextProg))
}

func TestWasmIoErrorOtherTextPreview1(t *testing.T) {
	ioErrorTextRun(t, buildWasmCore(t, ioErrorTextProg))
}
