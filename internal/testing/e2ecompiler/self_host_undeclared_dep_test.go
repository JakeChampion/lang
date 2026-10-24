package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostUndeclaredDependencyIsE086(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"fern.toml": "[package]\nname = \"app\"\n",
		"main.fern": "import \"textkit\";\nfunction main(): i32 { return 0; }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const want = `main.fern:1:1: error[E086]: cannot resolve import "textkit": not found, and "textkit" is not a declared dependency in `
	for _, c := range []struct {
		name string
		cmd  *exec.Cmd
	}{
		{"self-host", exec.Command(e2eharness.SelfHostCLI(t), "-check", "main.fern", e2eharness.SelfHostStdlibRoot(t))},
		{"oracle", exec.Command(buildFernCLIBin(t), "-check", "main.fern")},
	} {
		c.cmd.Dir = dir
		out, _ := c.cmd.CombinedOutput()
		if c.cmd.ProcessState.ExitCode() == 0 || !strings.Contains(string(out), want) {
			t.Errorf("%s: -check exit = %d, want a refusal containing %q\n%s", c.name, c.cmd.ProcessState.ExitCode(), want, out)
		}
	}
}
