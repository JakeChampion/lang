package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Executables require an entry point. Assembly output also accepts libraries.
func TestSelfHostMainlessProgramRequiresEntry(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const source = "function f(): i32 {\n  return 1;\n}\n"
	src := filepath.Join(t.TempDir(), "library.fern")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, env := range [][]string{nil, {"FERN_LEAKCHECK=1"}} {
				bin := filepath.Join(t.TempDir(), "library")
				cmd := cli.compileCmd(target, bin, src)
				cmd.Env = append(os.Environ(), env...)
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), "error[E001]: an executable requires a top-level `main` or `handle`") {
					t.Fatalf("compile: %v\n%s\nwant E001 for the missing entry point", err, out)
				}
				if _, err := os.Stat(bin); !os.IsNotExist(err) {
					t.Fatalf("refused executable exists: %v", err)
				}
				cmd = cli.compileCmd(target, bin, src, "-emit", "asm")
				cmd.Env = append(os.Environ(), env...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("assembly library: %v\n%s", err, out)
				}
			}
		})
	}
}
