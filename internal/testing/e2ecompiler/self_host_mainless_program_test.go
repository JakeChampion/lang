package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostMainlessExecutableIsRefused(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte("function f(): i32 {\n  return 1;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "error[E001]: an executable requires a top-level `main` or `handle`"
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			out, err := cli.compileCmd(target, filepath.Join(t.TempDir(), "prog"), src).CombinedOutput()
			if err == nil || !strings.Contains(string(out), want) {
				t.Errorf("build err = %v, want a refusal containing %q\n%s", err, want, out)
			}
		})
	}
}
