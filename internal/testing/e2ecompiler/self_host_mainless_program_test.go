package e2ecompiler

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// A module with no `main` and no top-level statements is a library compiled
// on its own: every target builds it, and its entry exits 0 (#12909). arm64
// goes through the self-host's own assembler, the path that refused the
// missing `__fn_main` label.
func TestSelfHostMainlessProgramExitsZero(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const source = "function f(): i32 {\n  return 1;\n}\n"
	for _, env := range [][]string{nil, {"FERN_LEAKCHECK=1"}} {
		name := "plain"
		if env != nil {
			name = "leakcheck"
		}
		t.Run(name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, source, target, env...); code != 0 {
					t.Errorf("%s: exit %d, stderr %q; want 0", target, code, stderr)
				}
			}
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, qemu := arm64Tooling(t)
			cmd := runArm64Bin(qemu, cli.arm64Binary(t, src, env...))
			var eb bytes.Buffer
			cmd.Stderr = &eb
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("arm64-linux: exit %d, stderr %q; want 0", code, eb.String())
			}
		})
	}
}
