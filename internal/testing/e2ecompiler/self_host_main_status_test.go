package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Every result type E084 allows `main` maps to the same process status in
// both interpreters: an integer of any width is its low byte, a boolean 1 or
// 0.
func TestMainResultStatusAgrees(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, c := range []struct {
		name, src string
		want      int
	}{
		{"boolean-true", "function main(): boolean { return true; }\n", 1},
		{"boolean-false", "function main(): boolean { return false; }\n", 0},
		{"i64-low-byte", "function main(): i64 { return 5000000003 as i64; }\n", 3},
		{"u8", "function main(): u8 { return 255 as u8; }\n", 255},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for leg, cmd := range map[string]*exec.Cmd{
				"self-host": runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib),
				"oracle":    exec.Command(oracle, "-interp", path),
			} {
				out, _ := cmd.CombinedOutput()
				if code := cmd.ProcessState.ExitCode(); code != c.want {
					t.Errorf("%s: exit %d, want %d\n%s", leg, code, c.want, out)
				}
			}
		})
	}
}
