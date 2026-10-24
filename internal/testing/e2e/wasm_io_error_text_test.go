package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
