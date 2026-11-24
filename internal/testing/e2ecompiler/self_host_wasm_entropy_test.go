package e2ecompiler

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A wasm32-wasi program stops when random_get gives an error. The test puts a
// random_get that returns EIO (29) in place of the import, so the program
// must write the entropy diagnostic, trap, and print nothing. The control runs
// the same module with the import of the host.
func TestSelfHostWasmEntropyFailure(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	cli := buildSelfHostCLI(t)
	const randomGet = "  (import \"wasi_snapshot_preview1\" \"random_get\" (func $__fern_random_get (param i32 i32) (result i32)))\n"
	cases := []struct {
		name, body string
	}{
		{"random_bytes", "let b = rand.bytes(16);\n  print(\"drew \" + b.len().to_string());"},
		{"random_i32", "let v = rand.next_i32();\n  print(\"drew 4\");"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			prog := "import \"std/rand\";\n\nfunction main(): i32 {\n  " + c.body + "\n  return 0;\n}\n"
			if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
				t.Fatal(err)
			}
			watPath := cli.emit(t, src, "wasm32-wasi", "FERN_STRICT_IR=1")
			data, err := os.ReadFile(watPath)
			if err != nil {
				t.Fatal(err)
			}
			wat := string(data)
			if strings.Count(wat, randomGet) != 1 {
				t.Fatalf("cannot locate the random_get import")
			}
			run := func(path string) (string, string, int) {
				t.Helper()
				cmd := exec.Command(wasmtime, "run", "-W", "unknown-imports-trap", "--invoke", "main", path)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				code := 0
				if err := cmd.Run(); err != nil {
					var exit *exec.ExitError
					if !errors.As(err, &exit) {
						t.Fatal(err)
					}
					code = exit.ExitCode()
				}
				return stdout.String(), stderr.String(), code
			}
			if out, stderr, code := run(watPath); code != 0 || !strings.Contains(out, "drew ") {
				t.Fatalf("control: exit %d, stdout %q\n%s", code, out, stderr)
			}
			end := strings.LastIndex(wat, ")")
			failing := strings.Replace(wat[:end], randomGet, "", 1) + "(func $__fern_random_get (param i32 i32) (result i32) (i32.const 29))" + wat[end:]
			failPath := filepath.Join(t.TempDir(), "fail.wat")
			if err := os.WriteFile(failPath, []byte(failing), 0o644); err != nil {
				t.Fatal(err)
			}
			out, stderr, code := run(failPath)
			if code == 0 {
				t.Fatalf("the program returned after an entropy failure, stdout %q", out)
			}
			if strings.Contains(out, "drew ") {
				t.Errorf("the program used the bytes of a failed random_get: stdout %q", out)
			}
			if !strings.Contains(stderr, "fern: entropy unavailable") {
				t.Errorf("stderr has no entropy diagnostic:\n%s", stderr)
			}
		})
	}
}
